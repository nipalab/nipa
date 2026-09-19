package usecase

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/merge"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type mergeClient interface {
	Connect(ctx context.Context, host string) error
	GetTreeNodeManifest(ctx context.Context, org, project, branch, path string) (*serverDomain.TreeNode, error)
	DownloadChunks(ctx context.Context, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error
	GetMergeBase(ctx context.Context, org, project, target, source string) (*domain.MergeBaseInfo, error)
	MergeFastForward(ctx context.Context, org, project, target, source string) (*serverDomain.Branch, error)
}

type mergeLocalRepo interface {
	Init(target string) error
	LoadConfig() (*domain.Config, error)
	Snapshot() (*domain.Snapshot, error)
	ListStaged() ([]string, error)
	SaveTree(root *serverDomain.TreeNode) error
	SaveCommit(commitID, commitHash string) error
	SaveMergeState(state *domain.MergeState) error
	LoadMergeState() (*domain.MergeState, error)
	ClearMergeState() error
	ClearStaged() error
	StageAdd(path string) error
	MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error)
	StoreChunks(chunks []*serverDomain.ChunkData) error
	LoadChunk(hash serverDomain.Hash) ([]byte, error)
}

type MergeOptions struct {
	Abort   bool
	FFOnly  bool
	NoFF    bool
	Message string
}

type Outcome struct {
	UpToDate       bool
	FastForwarded  bool
	MergeCommitted bool
	Conflicts      []string
}

type Merge struct {
	auth      *Auth
	client    mergeClient
	localRepo mergeLocalRepo
	push      *Push
}

func NewMerge(auth *Auth, client mergeClient, localRepo mergeLocalRepo, push *Push) *Merge {
	return &Merge{
		auth:      auth,
		client:    client,
		localRepo: localRepo,
		push:      push,
	}
}

func (m *Merge) Run(ctx context.Context, root, sourceBranch string, opts MergeOptions) (*Outcome, error) {
	if err := m.localRepo.Init(root); err != nil {
		return nil, err
	}
	cfg, err := m.localRepo.LoadConfig()
	if err != nil {
		return nil, err
	}
	nipaUrl, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return nil, err
	}
	if nipaUrl.Path != "" {
		return nil, domain.NewUserError("merging in a subdirectory clone is not supported yet")
	}

	if opts.Abort {
		return m.abort(ctx, root, nipaUrl, cfg.Branch)
	}
	if strings.TrimSpace(sourceBranch) == "" {
		return nil, domain.NewUserError("a source branch is required to merge")
	}
	if opts.FFOnly && opts.NoFF {
		return nil, domain.NewUserError("--ff-only and --no-ff are mutually exclusive")
	}

	if err := m.client.Connect(ctx, nipaUrl.Host); err != nil {
		return nil, err
	}
	if err := m.auth.MakeSureLoggedIn(ctx, nipaUrl.Host); err != nil {
		return nil, err
	}

	pending, err := m.localRepo.LoadMergeState()
	if err != nil {
		return nil, err
	}
	if pending != nil {
		return nil, domain.NewUserError("a merge is already in progress; resolve the conflicts and push, or run nipa merge --abort")
	}

	staged, err := m.localRepo.ListStaged()
	if err != nil {
		return nil, err
	}
	if len(staged) > 0 {
		return nil, domain.NewUserError("cannot merge with staged changes; push or reset them first")
	}

	info, err := m.client.GetMergeBase(ctx, nipaUrl.Org, nipaUrl.Project, cfg.Branch, sourceBranch)
	if err != nil {
		return nil, err
	}
	if info.SourceCommitID == "" {
		return nil, domain.NewUserError(fmt.Sprintf("branch %q has no commits to merge", sourceBranch))
	}
	if info.MergeBaseCommitID == info.SourceCommitID {
		return &Outcome{UpToDate: true}, nil
	}

	ffPossible := info.MergeBaseCommitID == info.TargetCommitID
	if opts.FFOnly && !ffPossible {
		return nil, domain.NewUserError("cannot fast-forward; the target branch has moved since the branches diverged")
	}
	if ffPossible && !opts.NoFF {
		return m.fastForward(ctx, root, nipaUrl, cfg.Branch, sourceBranch, info.SourceCommitHash)
	}

	return m.trueMerge(ctx, root, nipaUrl, cfg.Branch, sourceBranch, info, opts)
}

func (m *Merge) fastForward(ctx context.Context, root string, url *domain.NipaUrl, targetBranch, sourceBranch, sourceCommitHash string) (*Outcome, error) {
	branch, err := m.client.MergeFastForward(ctx, url.Org, url.Project, targetBranch, sourceBranch)
	if err != nil {
		return nil, err
	}
	targetTree, err := m.client.GetTreeNodeManifest(ctx, url.Org, url.Project, targetBranch, "")
	if err != nil {
		return nil, err
	}
	if err := syncWorkingCopy(ctx, m.client, m.localRepo, root, targetTree); err != nil {
		return nil, err
	}
	if err := m.localRepo.SaveTree(targetTree); err != nil {
		return nil, err
	}
	if err := m.localRepo.SaveCommit(commitIDString(branch), sourceCommitHash); err != nil {
		return nil, err
	}
	return &Outcome{FastForwarded: true}, nil
}

func (m *Merge) trueMerge(ctx context.Context, root string, url *domain.NipaUrl, targetBranch, sourceBranch string, info *domain.MergeBaseInfo, opts MergeOptions) (*Outcome, error) {
	base := merge.Flatten(info.MergeBaseTree)
	targetTree, err := m.client.GetTreeNodeManifest(ctx, url.Org, url.Project, targetBranch, "")
	if err != nil {
		return nil, err
	}
	sourceTree, err := m.client.GetTreeNodeManifest(ctx, url.Org, url.Project, sourceBranch, "")
	if err != nil {
		return nil, err
	}
	ours := merge.Flatten(targetTree)
	theirs := merge.Flatten(sourceTree)
	res := merge.ThreeWay(base, ours, theirs)

	var needs []merge.File
	for _, e := range res.Entries {
		switch e.Decision {
		case merge.KeepTheirs:
			needs = append(needs, e.Theirs)
		case merge.TextMerge:
			needs = append(needs, e.Base, e.Ours, e.Theirs)
		}
	}
	var want []serverDomain.Hash
	for _, f := range needs {
		want = append(want, f.ChunkHashes...)
	}
	missing, err := m.localRepo.MissingChunks(want)
	if err != nil {
		return nil, err
	}
	if err := downloadMissing(ctx, m.client, m.localRepo, missing, estimatedBytes(needs, missing)); err != nil {
		return nil, err
	}

	snapshot, err := m.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	baseByPath := make(map[string]domain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		baseByPath[f.Path] = f
	}

	finalFiles := make(map[string]merge.File)
	var stagedPaths, conflictedPaths, deletedPaths []string

	for _, p := range sortedEntryPaths(res.Entries) {
		e := res.Entries[p]
		switch e.Decision {
		case merge.KeepOurs:
			finalFiles[p] = e.Ours

		case merge.KeepTheirs:
			if err := materializeFile(root, toMaterialized(e.Theirs), m.localRepo.LoadChunk); err != nil {
				return nil, err
			}
			finalFiles[p] = e.Theirs
			stagedPaths = append(stagedPaths, p)

		case merge.TextMerge:
			baseContent, err := loadFileContent(m.localRepo.LoadChunk, e.Base)
			if err != nil {
				return nil, err
			}
			oursContent, err := loadFileContent(m.localRepo.LoadChunk, e.Ours)
			if err != nil {
				return nil, err
			}
			theirsContent, err := loadFileContent(m.localRepo.LoadChunk, e.Theirs)
			if err != nil {
				return nil, err
			}
			merged, wasConflict := merge.MergeText(baseContent, oursContent, theirsContent)
			mf, err := m.storeMergedFile(p, merged, e.Ours.Mode, e.Ours.IsBinary || e.Theirs.IsBinary)
			if err != nil {
				return nil, err
			}
			if err := materializeFile(root, toMaterialized(mf), m.localRepo.LoadChunk); err != nil {
				return nil, err
			}
			finalFiles[p] = mf
			stagedPaths = append(stagedPaths, p)
			if wasConflict {
				conflictedPaths = append(conflictedPaths, p)
			}

		case merge.BinaryConflict, merge.AddAddConflict:
			// Keep ours on disk (no markers in binaries); record the conflict.
			finalFiles[p] = e.Ours
			conflictedPaths = append(conflictedPaths, p)

		case merge.ModifyDeleteConflict:
			// Ours kept: the file stays as the target had it.
			finalFiles[p] = e.Ours
			conflictedPaths = append(conflictedPaths, p)

		case merge.DeleteModifyConflict:
			// Ours deleted the file; leave the working copy as-is.
			conflictedPaths = append(conflictedPaths, p)
		}
	}

	for _, p := range sortedStrings(res.Deleted) {
		base, ok := baseByPath[p]
		if !ok {
			continue
		}
		removed, err := guardedRemove(root, base)
		if err != nil {
			return nil, fmt.Errorf("remove %s: %w", p, err)
		}
		if removed {
			deletedPaths = append(deletedPaths, p)
			stagedPaths = append(stagedPaths, p)
		}
	}

	for _, p := range stagedPaths {
		if err := m.localRepo.StageAdd(p); err != nil {
			return nil, err
		}
	}

	outcome := &Outcome{Conflicts: conflictedPaths}
	state := &domain.MergeState{
		SourceBranch:     sourceBranch,
		SourceCommitID:   info.SourceCommitID,
		SourceCommitHash: info.SourceCommitHash,
		BaseCommitID:     info.MergeBaseCommitID,
		BaseTreeHash:     baseTreeHash(info),
		TargetTreeHash:   targetTree.Hash.String(),
		Conflicts:        conflictedPaths,
	}
	if len(conflictedPaths) > 0 {
		if err := m.localRepo.SaveTree(treeFromFiles(finalFiles)); err != nil {
			return nil, err
		}
		if err := m.localRepo.SaveMergeState(state); err != nil {
			return nil, err
		}
		return outcome, nil
	}

	if err := m.localRepo.SaveMergeState(state); err != nil {
		return nil, err
	}
	msg := opts.Message
	if strings.TrimSpace(msg) == "" {
		msg = fmt.Sprintf("Merge branch '%s' into '%s'", sourceBranch, targetBranch)
	}
	if err := m.push.Run(ctx, root, msg); err != nil {
		return nil, err
	}
	outcome.MergeCommitted = true
	return outcome, nil
}

func (m *Merge) abort(ctx context.Context, root string, url *domain.NipaUrl, branch string) (*Outcome, error) {
	pending, err := m.localRepo.LoadMergeState()
	if err != nil {
		return nil, err
	}
	if pending == nil {
		return nil, domain.NewUserError("no merge in progress to abort")
	}
	if err := m.client.Connect(ctx, url.Host); err != nil {
		return nil, err
	}
	if err := m.auth.MakeSureLoggedIn(ctx, url.Host); err != nil {
		return nil, err
	}
	targetTree, err := m.client.GetTreeNodeManifest(ctx, url.Org, url.Project, branch, "")
	if err != nil {
		return nil, err
	}
	if err := syncWorkingCopy(ctx, m.client, m.localRepo, root, targetTree); err != nil {
		return nil, err
	}
	if err := m.localRepo.SaveTree(targetTree); err != nil {
		return nil, err
	}
	if err := m.localRepo.SaveCommit("", ""); err != nil {
		return nil, err
	}
	if err := m.localRepo.ClearStaged(); err != nil {
		return nil, err
	}
	if err := m.localRepo.ClearMergeState(); err != nil {
		return nil, err
	}
	return &Outcome{}, nil
}

func (m *Merge) storeMergedFile(path string, data []byte, mode int, isBinary bool) (merge.File, error) {
	var hashes []serverDomain.Hash
	var sizes []int64
	batch := make([]*serverDomain.ChunkData, 0, 16)
	err := chunker.Scan(bytes.NewReader(data), func(c chunker.Chunk) error {
		hashes = append(hashes, c.Hash)
		sizes = append(sizes, int64(len(c.Data)))
		batch = append(batch, &serverDomain.ChunkData{Hash: c.Hash, Data: c.Data})
		return nil
	})
	if err != nil {
		return merge.File{}, err
	}
	if err := m.localRepo.StoreChunks(batch); err != nil {
		return merge.File{}, err
	}
	return merge.File{
		Path:        path,
		Mode:        mode,
		SizeBytes:   int64(len(data)),
		IsBinary:    isBinary || chunker.IsBinary(data),
		Hash:        chunker.FileHash(hashes),
		ChunkHashes: hashes,
		ChunkSizes:  sizes,
	}, nil
}

func commitIDString(b *serverDomain.Branch) string {
	if b == nil || b.CommitID == nil {
		return ""
	}
	return b.CommitID.Base36()
}

func baseTreeHash(info *domain.MergeBaseInfo) string {
	if info.MergeBaseTree == nil {
		return ""
	}
	return info.MergeBaseTree.Hash.String()
}

func sortedPaths(files map[string]merge.File) []string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func sortedEntryPaths(entries map[string]merge.Entry) []string {
	paths := make([]string, 0, len(entries))
	for p := range entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func treeFromFiles(files map[string]merge.File) *serverDomain.TreeNode {
	root := &serverDomain.TreeNode{Name: "", Mode: 0o040000}
	cache := map[string]*nodeRef{"": {node: root}}
	for _, p := range sortedPaths(files) {
		parts := strings.Split(p, "/")
		cur := cache[""]
		prefix := ""
		for _, part := range parts[:len(parts)-1] {
			key := prefix + "/" + part
			if prefix == "" {
				key = part
			}
			next, ok := cache[key]
			if !ok {
				t := &serverDomain.TreeNode{Name: part, Mode: 0o040000}
				next = &nodeRef{node: t}
				cache[key] = next
				cur.node.TreeChildren = append(cur.node.TreeChildren, t)
			}
			cur = next
			prefix = key
		}
		f := files[p]
		cur.node.FileChildren = append(cur.node.FileChildren, &serverDomain.File{
			Hash:      f.Hash,
			Name:      f.Path[strings.LastIndex(f.Path, "/")+1:],
			Mode:      f.Mode,
			SizeBytes: f.SizeBytes,
			IsBinary:  f.IsBinary,
			Chunks:    chunksOf(f),
		})
	}
	return root
}

type nodeRef struct {
	node *serverDomain.TreeNode
}

func chunksOf(f merge.File) []serverDomain.Chunk {
	chunks := make([]serverDomain.Chunk, len(f.ChunkHashes))
	for i, h := range f.ChunkHashes {
		size := int64(0)
		if i < len(f.ChunkSizes) {
			size = f.ChunkSizes[i]
		}
		chunks[i] = serverDomain.Chunk{Hash: h, SizeBytes: size}
	}
	return chunks
}

func loadFileContent(loadChunk func(serverDomain.Hash) ([]byte, error), f merge.File) ([]byte, error) {
	var out []byte
	for _, h := range f.ChunkHashes {
		data, err := loadChunk(h)
		if err != nil {
			return nil, fmt.Errorf("load chunk %s: %w", h, err)
		}
		out = append(out, data...)
	}
	return out, nil
}

func toMaterialized(f merge.File) materializedFile {
	return materializedFile{
		Path:        f.Path,
		Mode:        f.Mode,
		SizeBytes:   f.SizeBytes,
		FileHash:    f.Hash,
		ChunkHashes: f.ChunkHashes,
	}
}

func estimatedBytes(files []merge.File, missing []serverDomain.Hash) int64 {
	set := make(map[serverDomain.Hash]struct{}, len(missing))
	for _, h := range missing {
		set[h] = struct{}{}
	}
	var total int64
	for _, f := range files {
		for _, h := range f.ChunkHashes {
			if _, ok := set[h]; ok {
				total += f.SizeBytes
				break
			}
		}
	}
	return total
}
