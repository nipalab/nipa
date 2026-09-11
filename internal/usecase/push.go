package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=push_mock_test.go -package=usecase
type pushRepository interface {
	ApplyPush(ctx context.Context, req ApplyPushRequest) error
}

type ApplyPushRequest struct {
	ProjectID  snow.ID
	BranchID   snow.ID
	CommitID   snow.ID
	CommitHash domain.Hash
	ParentID   *snow.ID
	UserID     snow.ID
	Message    string
	Nodes      []PushNodeRow
	Files      []PushFileRow
}

// PushNodeRow is a new tree node (directory). IDs are logical and parent-first;
// the repository translates them to real row IDs.
type PushNodeRow struct {
	ID       int64
	Hash     domain.Hash
	Name     string
	Mode     int
	ParentID *int64
}

// PushFileRow is a new file row attached to the node with the given logical ID.
type PushFileRow struct {
	Name        string
	Mode        int
	SizeBytes   int64
	IsBinary    bool
	Hash        domain.Hash
	TreeID      int64
	ChunkHashes []domain.Hash
}

type Push struct {
	permUc     permissionUsecase
	branchRepo branchRepository
	pushRepo   pushRepository
	snowNode   snow.Node
}

func NewPush(permUc permissionUsecase, branchRepo branchRepository, pushRepo pushRepository, snowNode snow.Node) *Push {
	return &Push{
		permUc:     permUc,
		branchRepo: branchRepo,
		pushRepo:   pushRepo,
		snowNode:   snowNode,
	}
}

func (p *Push) Push(ctx context.Context, projectID snow.ID, branchName, baseTreeHash, message string, files []*domain.PushFile, removed []string) (*domain.PushResult, error) {
	if !p.permUc.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	if strings.TrimSpace(message) == "" {
		return nil, domain.NewErrorUser("commit message is required")
	}
	if err := validatePushFiles(files); err != nil {
		return nil, err
	}
	if err := validateRemovedFiles(removed, files); err != nil {
		return nil, err
	}
	for _, f := range files {
		if got := treehash.FileHash(f.ChunkHashes); got != f.FileHash {
			return nil, domain.NewErrorUser(fmt.Sprintf("file hash mismatch for %q", f.Path))
		}
	}

	branch, err := p.branchRepo.GetBranchByName(ctx, projectID, branchName)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", branchName))
	}
	if err != nil {
		return nil, err
	}
	if branch.IsProtected {
		//TODO: add proper error for client side
		return nil, domain.NewErrorNoPermission()
	}

	var headCommit *domain.Commit
	if branch.CommitID != nil {
		if headCommit, err = p.branchRepo.GetCommit(ctx, *branch.CommitID); err != nil {
			return nil, err
		}
	}

	headTreeHash, err := p.headTreeHash(ctx, headCommit)
	if err != nil {
		return nil, err
	}
	if baseTreeHash == "" {
		baseTreeHash = treehash.TreeHash(nil, nil).String()
	}
	if !strings.EqualFold(baseTreeHash, headTreeHash.String()) {
		return nil, domain.NewErrorConflict(fmt.Sprintf("branch %q has moved: expected base tree hash %s, current head is %s", branchName, baseTreeHash, headTreeHash))
	}

	root, err := buildNewTree(ctx, p.branchRepo, headCommit, files, removed)
	if err != nil {
		return nil, err
	}
	req := p.toApplyRequest(ctx, projectID, branch, headCommit, message, root)

	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	req.UserID = claim.UserID

	if err := p.pushRepo.ApplyPush(ctx, req); err != nil {
		return nil, err
	}
	return &domain.PushResult{
		CommitID:   req.CommitID,
		CommitHash: req.CommitHash,
		TreeHash:   root.Hash,
	}, nil
}

func (p *Push) headTreeHash(ctx context.Context, headCommit *domain.Commit) (domain.Hash, error) {
	if headCommit == nil {
		return treehash.TreeHash(nil, nil), nil
	}
	root, err := p.branchRepo.GetTreeNode(ctx, headCommit.TreeID)
	if err != nil {
		return domain.Hash{}, err
	}
	return root.Hash, nil
}

func (p *Push) toApplyRequest(ctx context.Context, projectID snow.ID, branch *domain.Branch, headCommit *domain.Commit, message string, root *pushTreeNode) ApplyPushRequest {
	var parentHashes []domain.Hash
	var parentID *snow.ID
	if headCommit != nil {
		parentHashes = []domain.Hash{headCommit.Hash}
		pid := branch.CommitID
		parentID = pid
	}

	req := ApplyPushRequest{
		ProjectID:  projectID,
		BranchID:   branch.ID,
		CommitID:   p.snowNode.Generate(),
		CommitHash: treehash.CommitHash(root.Hash, parentHashes, message),
		ParentID:   parentID,
		Message:    message,
	}

	queue := []*pushTreeNode{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		var parentLogicalID *int64
		if n.Parent != nil {
			id := n.Parent.ID
			parentLogicalID = &id
		}
		req.Nodes = append(req.Nodes, PushNodeRow{
			ID:       n.ID,
			Hash:     n.Hash,
			Name:     n.Name,
			Mode:     n.Mode,
			ParentID: parentLogicalID,
		})
		for _, f := range n.Files {
			req.Files = append(req.Files, PushFileRow{
				Name:        f.Name,
				Mode:        f.Mode,
				SizeBytes:   f.SizeBytes,
				IsBinary:    f.IsBinary,
				Hash:        f.Hash,
				TreeID:      n.ID,
				ChunkHashes: f.ChunkHashes,
			})
		}
		queue = append(queue, n.Children...)
	}
	return req
}

func validatePushFiles(files []*domain.PushFile) error {
	seen := map[string]struct{}{}
	for _, f := range files {
		if err := validateRepoPath(f.Path); err != nil {
			return err
		}
		if _, ok := seen[f.Path]; ok {
			return domain.NewErrorUser(fmt.Sprintf("duplicate file path %q", f.Path))
		}
		seen[f.Path] = struct{}{}
		if len(f.ChunkHashes) == 0 {
			return domain.NewErrorUser(fmt.Sprintf("file %q has no chunks", f.Path))
		}
	}
	return nil
}

func validateRemovedFiles(removed []string, files []*domain.PushFile) error {
	changed := map[string]struct{}{}
	for _, f := range files {
		changed[f.Path] = struct{}{}
	}
	seen := map[string]bool{}
	for _, r := range removed {
		if err := validateRepoPath(r); err != nil {
			return err
		}
		if _, ok := changed[r]; ok {
			return domain.NewErrorUser(fmt.Sprintf("path %q is both pushed and removed", r))
		}
		if seen[r] {
			return domain.NewErrorUser(fmt.Sprintf("duplicate removed path %q", r))
		}
		seen[r] = true
	}
	return nil
}

func validateRepoPath(p string) error {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return domain.NewErrorUser("file path must not be empty")
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return domain.NewErrorUser(fmt.Sprintf("invalid file path %q", p))
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return domain.NewErrorUser(fmt.Sprintf("invalid file path %q", p))
		}
	}
	return nil
}

type pushTreeNode struct {
	ID       int64
	Parent   *pushTreeNode
	Hash     domain.Hash
	Name     string
	Mode     int
	Files    []*pushFile
	Children []*pushTreeNode
	KeepDirs []pushTreeEntry
}

type pushTreeEntry struct {
	Name string
	Hash domain.Hash
}

type pushFile struct {
	Name        string
	Mode        int
	SizeBytes   int64
	IsBinary    bool
	Hash        domain.Hash
	ChunkHashes []domain.Hash
}

func buildNewTree(ctx context.Context, branchRepo branchRepository, headCommit *domain.Commit, files []*domain.PushFile, removed []string) (*pushTreeNode, error) {
	b := &treeBuilder{
		branchRepo:  branchRepo,
		changed:     map[string][]*domain.PushFile{},
		removed:     map[string]map[string]bool{},
		rebuildDirs: map[string]bool{},
	}
	if headCommit != nil {
		root, err := branchRepo.GetTreeNode(ctx, headCommit.TreeID)
		if err != nil {
			return nil, err
		}
		b.baseRoot = root
	}

	addAncestors := func(dirPath string) {
		for d := dirPath; ; {
			b.rebuildDirs[d] = true
			i := strings.LastIndex(d, "/")
			if i < 0 {
				break
			}
			d = d[:i]
		}
	}
	for _, f := range files {
		d := dirOf(f.Path)
		b.changed[d] = append(b.changed[d], f)
		addAncestors(d)
	}
	for _, r := range removed {
		d := dirOf(r)
		if b.removed[d] == nil {
			b.removed[d] = map[string]bool{}
		}
		b.removed[d][baseOf(r)] = true
		addAncestors(d)
	}
	b.rebuildDirs[""] = true

	root, err := b.build(ctx, "", b.baseRoot)
	if err != nil {
		return nil, err
	}
	b.finalize(ctx, root)
	return root, nil
}

type treeBuilder struct {
	branchRepo  branchRepository
	baseRoot    *domain.TreeNode
	changed     map[string][]*domain.PushFile
	removed     map[string]map[string]bool
	rebuildDirs map[string]bool
	nextID      int64
}

func (b *treeBuilder) build(ctx context.Context, dirPath string, baseNode *domain.TreeNode) (*pushTreeNode, error) {
	b.nextID++
	node := &pushTreeNode{
		ID:   b.nextID,
		Name: nodeName(dirPath),
		Mode: 0o444,
	}

	var baseFiles []*domain.File
	var baseChildren []*domain.TreeNode
	if baseNode != nil {
		var err error
		if baseFiles, err = b.branchRepo.ListFilesByTree(ctx, baseNode.ID); err != nil {
			return nil, err
		}
		if baseChildren, err = b.branchRepo.ListTreeChildren(ctx, baseNode.ID); err != nil {
			return nil, err
		}
	}

	byName := map[string]*pushFile{}
	for _, pf := range b.changed[dirPath] {
		name := baseOf(pf.Path)
		byName[name] = &pushFile{
			Name:        name,
			Mode:        pf.Mode,
			SizeBytes:   pf.SizeBytes,
			IsBinary:    pf.IsBinary,
			Hash:        pf.FileHash,
			ChunkHashes: pf.ChunkHashes,
		}
	}
	removedSet := b.removed[dirPath]
	for _, bf := range baseFiles {
		if removedSet[bf.Name] {
			continue
		}
		if _, overridden := byName[bf.Name]; overridden {
			continue
		}
		chunks := make([]domain.Hash, 0, len(bf.Chunks))
		for _, c := range bf.Chunks {
			chunks = append(chunks, c.Hash)
		}
		byName[bf.Name] = &pushFile{
			Name:        bf.Name,
			Mode:        bf.Mode,
			SizeBytes:   bf.SizeBytes,
			IsBinary:    bf.IsBinary,
			Hash:        bf.Hash,
			ChunkHashes: chunks,
		}
	}
	for _, f := range byName {
		node.Files = append(node.Files, f)
	}
	sort.Slice(node.Files, func(i, j int) bool { return node.Files[i].Name < node.Files[j].Name })

	baseChildByName := map[string]*domain.TreeNode{}
	for _, c := range baseChildren {
		baseChildByName[c.Name] = c
	}

	for _, sub := range immediateSubdirs(b.rebuildDirs, dirPath) {
		childBase := baseChildByName[sub]
		child, err := b.build(ctx, joinPath(dirPath, sub), childBase)
		if err != nil {
			return nil, err
		}
		child.Parent = node
		node.Children = append(node.Children, child)
	}
	sort.Slice(node.Children, func(i, j int) bool { return node.Children[i].Name < node.Children[j].Name })

	for _, c := range baseChildren {
		sub := joinPath(dirPath, c.Name)
		if b.rebuildDirs[sub] {
			continue
		}
		node.KeepDirs = append(node.KeepDirs, pushTreeEntry{Name: c.Name, Hash: c.Hash})
	}
	return node, nil
}

func (b *treeBuilder) finalize(ctx context.Context, node *pushTreeNode) domain.Hash {
	files := make([]treehash.FileEntry, 0, len(node.Files))
	for _, f := range node.Files {
		files = append(files, treehash.FileEntry{Name: f.Name, Mode: f.Mode, Hash: f.Hash})
	}
	trees := make([]treehash.TreeEntry, 0, len(node.KeepDirs)+len(node.Children))
	for _, k := range node.KeepDirs {
		trees = append(trees, treehash.TreeEntry{Name: k.Name, Hash: k.Hash})
	}
	for _, c := range node.Children {
		trees = append(trees, treehash.TreeEntry{Name: c.Name, Hash: b.finalize(ctx, c)})
	}
	node.Hash = treehash.TreeHash(files, trees)
	return node.Hash
}

func dirOf(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	return p[:i]
}

func baseOf(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return p
	}
	return p[i+1:]
}

func nodeName(dirPath string) string {
	if dirPath == "" {
		return "root"
	}
	return baseOf(dirPath)
}

func joinPath(dirPath, name string) string {
	if dirPath == "" {
		return name
	}
	return dirPath + "/" + name
}

func immediateSubdirs(rebuildDirs map[string]bool, dirPath string) []string {
	prefix := ""
	if dirPath != "" {
		prefix = dirPath + "/"
	}
	set := map[string]bool{}
	for d := range rebuildDirs {
		if !strings.HasPrefix(d, prefix) {
			continue
		}
		rest := strings.TrimPrefix(d, prefix)
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		set[rest] = true
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
