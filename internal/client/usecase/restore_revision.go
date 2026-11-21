package usecase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type RevisionRestoreRepo interface {
	diffLocalRepo
	LoadMergeState() (*clientDomain.MergeState, error)
	LoadRevertState() (*clientDomain.RevertState, error)
	SnapshotFile(p string) (clientDomain.SnapshotFile, bool, error)
}

type revisionRestoreClient interface {
	Connect(ctx context.Context, host string) error
	GetCommitDirManifest(ctx context.Context, org, project, commitID, dir string) (*serverDomain.TreeNode, error)
	DownloadChunks(ctx context.Context, scope clientDomain.ChunkScope, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error
}

// RevisionRestore writes one file as it was in a commit over the working
// file ("get this version"). Nothing is staged: the file shows as modified.
type RevisionRestore struct {
	auth   *Auth
	client revisionRestoreClient
	repo   RevisionRestoreRepo
}

func NewRevisionRestore(auth *Auth, client revisionRestoreClient, repo RevisionRestoreRepo) *RevisionRestore {
	return &RevisionRestore{auth: auth, client: client, repo: repo}
}

func (r *RevisionRestore) Run(ctx context.Context, root, path, commitID string, progress ...DownloadProgress) (err error) {
	p, err := cleanBlobPath(path)
	if err != nil {
		return err
	}
	if err := checkWorkingPath(p); err != nil {
		return err
	}
	commitID = strings.TrimSpace(commitID)
	if _, perr := snow.ParseBase36(commitID); commitID == "" || perr != nil {
		return clientDomain.NewUserError("a commit id is required")
	}
	if err := r.repo.Init(root); err != nil {
		return err
	}
	// Only a restore that put content into the object cache has something to
	// prune.
	cached := false
	defer func() {
		if cached {
			pruneAfterSuccess(r.repo, err)
		}
	}()
	unlock, err := lockShared(r.repo)
	if err != nil {
		return err
	}
	defer unlock()
	if err := r.refusePending(); err != nil {
		return err
	}
	cfg, err := r.repo.LoadConfig()
	if err != nil {
		return err
	}
	if !sparseCovers(cfg.Sparse, p) {
		return clientDomain.NewUserError(fmt.Sprintf("%s is outside this clone's checkout; add its folder to the checkout first", p))
	}
	if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p))); err == nil && info.IsDir() {
		return clientDomain.NewUserError(fmt.Sprintf("%s is a folder", p))
	}
	if err := checkWriteInsideRoot(root, p); err != nil {
		return err
	}

	nu, err := clientDomain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return err
	}
	if err := r.client.Connect(ctx, nu.Origin); err != nil {
		return err
	}
	if err := r.auth.MakeSureLoggedIn(ctx, nu.Host); err != nil {
		return err
	}
	file, err := r.revisionFile(ctx, nu, commitID, p)
	if err != nil {
		return err
	}
	job := materializeJob{file: file}
	if tracked, ok, err := r.repo.SnapshotFile(p); err != nil {
		return err
	} else if ok {
		job.prevMode = tracked.Mode
	}
	scope := clientDomain.ChunkScope{Org: nu.Org, Project: nu.Project, CommitIDs: commitIDs(commitID), Paths: []string{p}}

	endWrite, err := beginWorkingWrite(r.repo, root)
	if err != nil {
		return err
	}
	defer endWrite()
	if directEligible(file, nil) {
		// The object cache never keeps a past revision, so raw content goes
		// straight into the working file whether or not the path is pinned.
		return restoreError(r.writeDirect(ctx, root, job, scope, progress...))
	}
	missing, err := r.repo.MissingChunks(file.ChunkHashes)
	if err != nil {
		return err
	}
	cached = len(missing) > 0
	if err := downloadMissing(ctx, r.client, r.repo, scope, missing, file.SizeBytes, progress...); err != nil {
		return err
	}
	if err := materializeFile(root, file, job.prevMode, r.repo.OpenChunk); err != nil {
		return fmt.Errorf("restore %s: %w", p, err)
	}
	return nil
}

// revisionFile reads p as the commit has it from the manifest of p's
// directory, so restoring one file never loads the commit's whole tree.
func (r *RevisionRestore) revisionFile(ctx context.Context, nu *clientDomain.NipaUrl, commitID, p string) (materializedFile, error) {
	tree, err := r.client.GetCommitDirManifest(ctx, nu.Org, nu.Project, commitID, parentDir(p))
	if isNotFoundError(err) && parentDir(p) != "" {
		// The commit or the directory is missing; the commit's root tells
		// which.
		if _, rootErr := r.client.GetCommitDirManifest(ctx, nu.Org, nu.Project, commitID, ""); rootErr == nil {
			return materializedFile{}, missingFile(fmt.Sprintf("%s does not exist in %s", p, commitID))
		} else if !isNotFoundError(rootErr) {
			return materializedFile{}, rootErr
		}
	}
	if isNotFoundError(err) {
		return materializedFile{}, clientDomain.NewUserError(fmt.Sprintf("revision %q not found", commitID))
	}
	if err != nil {
		return materializedFile{}, err
	}
	var (
		file  materializedFile
		found bool
	)
	walkTreeFiles(tree, "", func(path string, f *serverDomain.File) {
		if path == p {
			file, found = materializedFromTree(path, f), true
		}
	})
	if !found {
		return materializedFile{}, missingFile(fmt.Sprintf("%s does not exist in %s", p, commitID))
	}
	return file, nil
}

// writeDirect assembles a raw file in a temp file next to its target, the
// way an update writes it: chunks the object cache holds are copied, the rest
// is streamed from the server into place, and the file is swapped in only
// when it is complete. Nothing passes through the object cache.
func (r *RevisionRestore) writeDirect(ctx context.Context, root string, job materializeJob, scope clientDomain.ChunkScope, progress ...DownloadProgress) error {
	plan, err := newDirectPlan(root, []materializeJob{job})
	if err != nil {
		return err
	}
	defer plan.cleanup()
	missing, err := r.repo.MissingChunks(job.file.ChunkHashes)
	if err != nil {
		return err
	}
	absent := make(map[serverDomain.Hash]struct{}, len(missing))
	for _, h := range missing {
		absent[h] = struct{}{}
	}
	local := make([]serverDomain.Hash, 0, len(plan.targets)-len(absent))
	for h := range plan.targets {
		if _, gone := absent[h]; !gone {
			local = append(local, h)
		}
	}
	if err := plan.copyLocal(ctx, local, r.repo.LoadChunk); err != nil {
		return err
	}
	sink := func(c *serverDomain.ChunkData) error { return plan.write(c.Hash, c.Data) }
	stream := &chunkStreamTarget{wants: plan.wants, receive: plan.receive, settle: plan.settle}
	if err := downloadChunksStream(ctx, r.client, scope, missing, job.file.SizeBytes, sink, stream, progress...); err != nil {
		return err
	}
	return plan.finish(ctx)
}

func (r *RevisionRestore) refusePending() error {
	merge, err := r.repo.LoadMergeState()
	if err != nil {
		return err
	}
	if merge != nil {
		return clientDomain.NewUserError("a merge is in progress; finish or abort it first")
	}
	revert, err := r.repo.LoadRevertState()
	if err != nil {
		return err
	}
	if revert != nil {
		return clientDomain.NewUserError("a revert is in progress; continue or abort it first")
	}
	return refuseShelfPending(r.repo)
}

func sparseCovers(prefixes []string, p string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, prefix := range prefixes {
		if serverDomain.PrefixCovers(prefix, p) {
			return true
		}
	}
	return false
}
