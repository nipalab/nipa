package daemon

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/preview"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// statusTTL bounds how long ListFiles reuses a status no change notice has
// invalidated, in case the watcher missed an event.
const statusTTL = 5 * time.Second

// RevisionRestorer writes one file of a commit over the working file.
type RevisionRestorer interface {
	Run(ctx context.Context, root, path, commitID string, progress ...usecase.DownloadProgress) error
}

// cachedStatus returns the working-copy status, reusing the last one while no
// change notice was published since and it is younger than statusTTL.
func (r *repo) cachedStatus(ctx context.Context) (*clientDomain.Status, error) {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	gen := r.changes.generation()
	if r.status != nil && r.statusGen == gen && time.Since(r.statusAt) < statusTTL {
		return r.status, nil
	}
	st, err := r.wc.Status(ctx)
	if err != nil {
		return nil, err
	}
	r.status, r.statusGen, r.statusAt = st, gen, time.Now()
	return st, nil
}

func (r *repo) rememberStatus(gen uint64, st *clientDomain.Status) {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	r.status, r.statusGen, r.statusAt = st, gen, time.Now()
}

// ListFiles lists one directory of the clone: the snapshot rows of that level
// merged with the working-copy status, plus untracked and newly added files
// and folders that only exist on disk.
func (s *Server) ListFiles(ctx context.Context, req *daemonpb.ListFilesRequest) (*daemonpb.ListFilesResponse, error) {
	dir, err := serverDomain.NormalizePathPrefix(req.GetDir())
	if err != nil {
		return nil, toStatusError(clientDomain.NewUserError(err.Error()))
	}
	if isMetadataDir(dir) {
		return nil, toStatusError(clientDomain.NewUserError("repository metadata cannot be listed"))
	}
	name := req.GetName()
	if strings.Contains(name, "/") {
		return nil, toStatusError(clientDomain.NewUserError("name must be a single path segment"))
	}
	var resp *daemonpb.ListFilesResponse
	err = s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		dirs, files, err := rp.localRepo.ListDir(dir)
		if err != nil {
			return err
		}
		st, err := rp.cachedStatus(ctx)
		if err != nil {
			return err
		}
		resp = &daemonpb.ListFilesResponse{Dir: dir, Entries: listEntries(rp.root, dir, dirs, files, st)}
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	if name != "" {
		for _, e := range resp.Entries {
			if e.GetName() == name {
				resp.Entries = []*daemonpb.FileEntry{e}
				return resp, nil
			}
		}
		return nil, status.Error(codes.NotFound, fmt.Sprintf("%s does not exist", joinPath(dir, name)))
	}
	return resp, nil
}

func isMetadataDir(dir string) bool {
	first, _, _ := strings.Cut(strings.ToLower(dir), "/")
	return first == ".nipa"
}

func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

type fileStates struct {
	status  map[string]string
	staged  map[string]bool
	changed map[string]bool
}

func newFileStates(st *clientDomain.Status) fileStates {
	fs := fileStates{status: map[string]string{}, staged: map[string]bool{}, changed: map[string]bool{}}
	mark := func(list []string, state string) {
		for _, p := range list {
			if _, ok := fs.status[p]; !ok {
				fs.status[p] = state
			}
		}
	}
	resolved := map[string]bool{}
	for _, p := range st.Resolved {
		resolved[p] = true
	}
	var open []string
	for _, p := range st.Conflicts {
		if !resolved[p] {
			open = append(open, p)
		}
	}
	mark(open, "conflict")
	mark(st.Deleted, "deleted")
	mark(st.Added, "added")
	mark(st.Missing, "missing")
	mark(st.Modified, "modified")
	mark(st.Staged, "modified")
	mark(st.Untracked, "untracked")
	for _, list := range [][]string{st.Staged, st.Deleted} {
		for _, p := range list {
			fs.staged[p] = true
		}
	}
	for p := range fs.status {
		for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
			if fs.changed[d] {
				break
			}
			fs.changed[d] = true
		}
	}
	return fs
}

func listEntries(root, dir string, dirs []string, files []clientDomain.SnapshotFile, st *clientDomain.Status) []*daemonpb.FileEntry {
	states := newFileStates(st)
	byName := map[string]*daemonpb.FileEntry{}
	var out []*daemonpb.FileEntry
	add := func(e *daemonpb.FileEntry) {
		byName[e.Name] = e
		out = append(out, e)
	}
	for _, name := range dirs {
		p := joinPath(dir, name)
		add(&daemonpb.FileEntry{Name: name, Path: p, IsDir: true, Tracked: true, ChildrenChanged: states.changed[p]})
	}
	for _, f := range files {
		e := &daemonpb.FileEntry{
			Name: path.Base(f.Path), Path: f.Path, SizeBytes: f.SizeBytes, IsBinary: f.IsBinary,
			Kind: string(preview.Classify(f.Path, f.IsBinary)), Status: states.status[f.Path], Staged: states.staged[f.Path],
			Tracked: true, Hash: f.Hash.String(),
		}
		if e.Status != "" {
			e.MtimeNs = mtime(root, f.Path)
		}
		add(e)
	}
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	for p, state := range states.status {
		if (state != "untracked" && state != "added") || !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		name, _, nested := strings.Cut(rest, "/")
		if existing, ok := byName[name]; ok {
			if existing.IsDir && !existing.Tracked && state == "added" {
				existing.Status = state
			}
			continue
		}
		if nested {
			add(&daemonpb.FileEntry{Name: name, Path: prefix + name, IsDir: true, Status: state, ChildrenChanged: true})
			continue
		}
		add(untrackedEntry(root, p, name, state, states.staged[p]))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func untrackedEntry(root, p, name, state string, staged bool) *daemonpb.FileEntry {
	e := &daemonpb.FileEntry{Name: name, Path: p, Status: state, Staged: staged}
	full := filepath.Join(root, filepath.FromSlash(p))
	if info, err := os.Stat(full); err == nil {
		e.SizeBytes = info.Size()
		e.MtimeNs = info.ModTime().UnixNano()
	}
	if f, err := os.Open(full); err == nil {
		e.IsBinary, _ = chunker.ProbeBinary(f)
		_ = f.Close()
	}
	e.Kind = string(preview.Classify(p, e.IsBinary))
	return e
}

func mtime(root, p string) int64 {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return 0
	}
	return info.ModTime().UnixNano()
}

// RestoreRevision writes the file as it was in a commit over the working
// file, under the exclusive slot. The file then shows as modified.
func (s *Server) RestoreRevision(req *daemonpb.RestoreRevisionRequest, stream daemonpb.NipaDaemon_RestoreRevisionServer) error {
	ctx := stream.Context()
	return s.runOp(ctx, req.GetRoot(), "restore", stream.Send, func(rp *repo, progress *progressAdapter) (*daemonpb.OpEvent, error) {
		if rp.restore == nil {
			return nil, status.Error(codes.FailedPrecondition, "restore is not configured")
		}
		defer rp.pruner.request()
		if err := rp.restore.Run(ctx, rp.root, req.GetPath(), req.GetCommitId(), progress); err != nil {
			return nil, err
		}
		return opResultEvent(&daemonpb.OpResult{Outcome: &daemonpb.OpResult_Restore{Restore: &daemonpb.RestoreRevisionResult{Path: req.GetPath(), CommitId: req.GetCommitId()}}}), nil
	})
}
