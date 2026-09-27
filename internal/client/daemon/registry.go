package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

// repo is one watched clone: a cached localrepo handle, its config, the
// per-root operation coordinator and the long-operation usecases (each with
// its own localrepo handle, like the CLI builds them per process).
type repo struct {
	root      string
	cfg       clientDomain.Config
	localRepo *localrepo.LocalRepo
	wc        *usecase.WorkingCopy
	coord     *coordinator

	update UpdateRunner
	push   PushRunner
	merge  MergeRunner
	revert RevertRunner
	diff   DiffRunner
	proxy  ProxyConn

	watcher    *watcher
	reconciler *reconciler

	refs int
}

func (r *repo) close() error {
	if r.reconciler != nil {
		r.reconciler.stop()
	}
	if r.watcher != nil {
		_ = r.watcher.Close()
	}
	if r.proxy != nil {
		_ = r.proxy.Close()
	}
	return r.localRepo.Close()
}

// startWatching starts the fsnotify accelerator. A failure only disables the
// accelerator: read-through status stays correct without it.
func (r *repo) startWatching() {
	src, err := newFsnotifySource(r.root)
	if err != nil {
		slog.Warn("daemon file watcher disabled", "root", r.root, "error", err)
		return
	}
	r.startWatchingWith(src, defaultWatchWindow)
}

func (r *repo) startWatchingWith(src eventSource, window time.Duration) {
	r.watcher = newWatcher(src, window)
	r.reconciler = startReconciler(r, r.watcher)
}

func (r *repo) headCommitID() string {
	commit, err := r.localRepo.LoadCommit()
	if err != nil {
		return ""
	}
	return commit.CommitID
}

func (r *repo) info() *daemonpb.RepoInfo {
	return &daemonpb.RepoInfo{
		Root:   r.root,
		Url:    r.cfg.Url,
		Branch: r.cfg.Branch,
		Sparse: r.cfg.Sparse,
	}
}

// registry tracks the clones this daemon serves. Lookups go through refs so
// unwatch can wait for in-flight operations before closing a handle; no new
// ref can be handed out once the root is removed from the map.
type registry struct {
	mu      sync.Mutex
	cond    *sync.Cond
	repos   map[string]*repo
	runners Runners
}

func newRegistry(runners Runners) *registry {
	r := &registry{repos: map[string]*repo{}, runners: runners}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// resolveRoot maps any path inside a clone to its repository root.
func resolveRoot(path string) (string, error) {
	if path == "" {
		return "", clientDomain.NewUserError("repository root is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dir := filepath.Clean(abs)
	for i := 0; i < localrepo.MaxSearchDepth; i++ {
		if _, err := os.Stat(filepath.Join(dir, localrepo.ConfigDir, localrepo.ConfigFile)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", clientDomain.NewUserError(fmt.Sprintf("%q is not a nipa working copy", path))
}

func openRepo(root string, runners Runners) (*repo, error) {
	lr := localrepo.NewLocalRepoWithTarget(root)
	cfg, err := lr.LoadConfig()
	if err != nil {
		return nil, clientDomain.NewUserError(fmt.Sprintf("read config of %q: %v", root, err))
	}
	wc, err := usecase.NewWorkingCopy(lr, root)
	if err != nil {
		_ = lr.Close()
		return nil, err
	}
	rp := &repo{
		root:      root,
		cfg:       *cfg,
		localRepo: lr,
		wc:        wc,
		coord:     newCoordinator(),
	}
	if runners.New != nil {
		ops := runners.New(root)
		rp.update = ops.Update
		rp.push = ops.Push
		rp.merge = ops.Merge
		rp.revert = ops.Revert
		rp.diff = ops.Diff
		rp.proxy = ops.Proxy
	}
	return rp, nil
}

// watch registers a clone (idempotently) and returns its cached handle.
func (r *registry) watch(path string) (*repo, error) {
	root, err := resolveRoot(path)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.repos[root]; ok {
		return existing, nil
	}
	opened, err := openRepo(root, r.runners)
	if err != nil {
		return nil, err
	}
	opened.startWatching()
	r.repos[root] = opened
	return opened, nil
}

// ref resolves a watched clone and pins it until the returned release runs.
func (r *registry) ref(path string) (*repo, func(), error) {
	root, err := resolveRoot(path)
	if err != nil {
		return nil, nil, err
	}
	r.mu.Lock()
	rp, ok := r.repos[root]
	if !ok {
		r.mu.Unlock()
		return nil, nil, clientDomain.NewUserError(fmt.Sprintf("repository %q is not watched", root))
	}
	rp.refs++
	r.mu.Unlock()
	return rp, func() {
		r.mu.Lock()
		rp.refs--
		if rp.refs == 0 {
			r.cond.Broadcast()
		}
		r.mu.Unlock()
	}, nil
}

// unwatch removes a clone and closes its handle after every in-flight
// operation released its ref. Unwatching an unknown root is a no-op.
func (r *registry) unwatch(path string) error {
	root, err := resolveRoot(path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	rp, ok := r.repos[root]
	if ok {
		delete(r.repos, root)
		for rp.refs > 0 {
			r.cond.Wait()
		}
	}
	r.mu.Unlock()
	if !ok {
		return nil
	}
	return rp.close()
}

func (r *registry) list() []*repo {
	r.mu.Lock()
	defer r.mu.Unlock()
	repos := make([]*repo, 0, len(r.repos))
	for _, rp := range r.repos {
		repos = append(repos, rp)
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].root < repos[j].root })
	return repos
}

// closeAll closes every cached handle; called after gRPC stops serving.
func (r *registry) closeAll() {
	r.mu.Lock()
	repos := r.repos
	r.repos = map[string]*repo{}
	r.mu.Unlock()
	for _, rp := range repos {
		_ = rp.close()
	}
}

// withRepo resolves a watched clone, admits the operation through the root's
// coordinator and keeps the handle pinned for the duration of fn.
func (r *registry) withRepo(ctx context.Context, path string, exclusive bool, fn func(*repo) error) error {
	rp, unref, err := r.ref(path)
	if err != nil {
		return err
	}
	defer unref()
	release, _, err := rp.coord.Acquire(ctx, exclusive)
	if err != nil {
		return err
	}
	defer release()
	return fn(rp)
}
