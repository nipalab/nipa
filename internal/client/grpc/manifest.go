package grpc

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/treedelta"
)

// StreamTreeManifest reads a manifest through the server-streaming RPC and
// rebuilds it as a GetTreeManifestResponse. The commit detail is set for a
// recursive request of a whole commit. Errors are the raw gRPC statuses.
func (c *Client) StreamTreeManifest(ctx context.Context, req *pb.GetTreeManifestRequest) (*pb.GetTreeManifestResponse, *pb.CommitDetail, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, nil, err
	}
	var res *pb.GetTreeManifestResponse
	var commit *pb.CommitDetail
	err = c.invokeWithThrottleRetry(ctx, func() error {
		return c.callAuthed(ctx, func(authCtx context.Context) error {
			var err error
			res, commit, err = readManifest(authCtx, client, req)
			return err
		})
	})
	if err != nil {
		return nil, nil, err
	}
	return res, commit, nil
}

// treeManifest reads a manifest straight into the domain tree. A large
// manifest is mostly chunk hashes, so it is not assembled as protobuf with
// hex strings first: the tree is the only copy held.
func (c *Client) treeManifest(ctx context.Context, req *pb.GetTreeManifestRequest) (*serverDomain.TreeNode, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, streamError(err, "streamed tree manifests")
	}
	var a treeAssembler
	err = c.invokeWithThrottleRetry(ctx, func() error {
		return c.callAuthed(ctx, func(authCtx context.Context) error {
			a = treeAssembler{}
			_, err := recvManifest(authCtx, client, req, a.add)
			return err
		})
	})
	if err != nil {
		return nil, streamError(err, "streamed tree manifests")
	}
	return a.finish(), nil
}

// streamError maps a streaming call's error to a domain error; a server that
// predates the RPC answers Unimplemented.
func streamError(err error, feature string) error {
	if status.Code(err) == codes.Unimplemented {
		return &domain.Error{Code: 400, Message: "the server does not support " + feature, Hint: "upgrade the nipa server to use this client"}
	}
	return toDomainError(err)
}

func readManifest(ctx context.Context, client pb.NipaServiceClient, req *pb.GetTreeManifestRequest) (*pb.GetTreeManifestResponse, *pb.CommitDetail, error) {
	var a manifestAssembler
	head, err := recvManifest(ctx, client, req, a.add)
	if err != nil {
		return nil, nil, err
	}
	return &pb.GetTreeManifestResponse{Branch: head.GetBranch(), RootTree: a.root}, head.GetCommit(), nil
}

// recvManifest feeds every entry of a streamed manifest to add and returns
// the first part, which carries the branch and commit.
func recvManifest(ctx context.Context, client pb.NipaServiceClient, req *pb.GetTreeManifestRequest, add func(*pb.TreeManifestEntry) error) (*pb.TreeManifestPart, error) {
	return recvManifestParts(ctx, client, req, nil, add)
}

// recvManifestParts is recvManifest with a hook that sees the head of the
// first part before any entry reaches add, for callers whose decoding depends
// on what the server chose to send.
func recvManifestParts(ctx context.Context, client pb.NipaServiceClient, req *pb.GetTreeManifestRequest, start func(*pb.TreeManifestPart) error, add func(*pb.TreeManifestEntry) error) (*pb.TreeManifestPart, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.StreamTreeManifest(ctx, req)
	if err != nil {
		return nil, err
	}
	var head *pb.TreeManifestPart
	for {
		part, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return head, nil
		}
		if err != nil {
			return nil, err
		}
		if head == nil {
			head = &pb.TreeManifestPart{
				Branch: part.GetBranch(), Commit: part.GetCommit(),
				View: part.GetView(), Delta: part.GetDelta(), TargetCommitId: part.GetTargetCommitId(),
			}
			if start != nil {
				if err := start(head); err != nil {
					return nil, err
				}
			}
		}
		for _, entry := range part.GetEntries() {
			if err := add(entry); err != nil {
				return nil, err
			}
		}
	}
}

// ManifestTarget names the commit a manifest is asked for: CommitID (base36)
// when set, the head of Branch otherwise.
type ManifestTarget struct {
	Branch   string
	CommitID string
}

// GetTreeManifestDelta reads the recursive manifest of target, as a delta
// against the manifest the caller holds when base names one and the server
// can build it. The answer is a full manifest (Manifest.Full) when base is
// empty, when the server declined (the base commit is unknown to it, or the
// view changed since the base was fetched: other read rules, another sparse
// set) and when the server predates deltas. Either way it carries the view
// token to store with the result. A delta is checked for shape only: applying
// it (treedelta.Apply) is what ties it to the base.
func (c *Client) GetTreeManifestDelta(ctx context.Context, org, project string, target ManifestTarget, paths []string, base treedelta.Base) (*treedelta.Manifest, error) {
	req := &pb.GetTreeManifestRequest{
		Context:   &pb.ProjectContext{Org: org, Project: project},
		Branch:    target.Branch,
		Paths:     paths,
		Recursive: true,
	}
	if target.CommitID != "" {
		req.CommitId = &target.CommitID
	}
	if base.CommitID != "" {
		req.BaseCommitId = &base.CommitID
		req.BaseView = base.View
		if base.TreeHash != nil {
			req.BaseTreeHash = base.TreeHash.Bytes()
		}
	}
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, streamError(err, "streamed tree manifests")
	}
	var out *treedelta.Manifest
	err = c.invokeWithThrottleRetry(ctx, func() error {
		return c.callAuthed(ctx, func(authCtx context.Context) error {
			var err error
			out, err = readManifestDelta(authCtx, client, req)
			return err
		})
	})
	if err != nil {
		return nil, streamError(err, "streamed tree manifests")
	}
	return out, nil
}

func readManifestDelta(ctx context.Context, client pb.NipaServiceClient, req *pb.GetTreeManifestRequest) (*treedelta.Manifest, error) {
	var full treeAssembler
	var delta *deltaAssembler
	head, err := recvManifestParts(ctx, client, req, func(head *pb.TreeManifestPart) error {
		if !head.GetDelta() {
			return nil
		}
		if req.GetBaseCommitId() == "" {
			return errors.New("malformed tree manifest: a delta nobody asked for")
		}
		delta = &deltaAssembler{}
		return nil
	}, func(entry *pb.TreeManifestEntry) error {
		if delta != nil {
			return delta.add(entry)
		}
		return full.add(entry)
	})
	if err != nil {
		return nil, err
	}
	out := &treedelta.Manifest{View: head.GetView()}
	if delta == nil {
		out.Full = full.finish()
		return out, nil
	}
	if out.Delta, err = delta.finish(); err != nil {
		return nil, err
	}
	out.CommitID = head.GetTargetCommitId()
	return out, nil
}

// deltaAssembler builds a treedelta.Delta from the entries of a delta
// manifest, with the checks treeAssembler applies to a full one: names are
// single safe path segments and hashes are whole. On top of that nothing may
// follow an unchanged directory or file, which carry a name only.
type deltaAssembler struct {
	root  *treedelta.Dir
	hash  *serverDomain.Hash
	stack []*treedelta.Dir
	// files reads the chunk lists of the file being received.
	files treeAssembler
}

func (a *deltaAssembler) add(entry *pb.TreeManifestEntry) error {
	switch e := entry.GetEntry().(type) {
	case *pb.TreeManifestEntry_Dir:
		return a.dir(e.Dir)
	case *pb.TreeManifestEntry_File:
		return a.file(e.File)
	case *pb.TreeManifestEntry_Chunks:
		if a.files.last == nil {
			return errors.New("malformed tree manifest: chunks before any file")
		}
		return a.files.chunks(e.Chunks.GetChunkHashes(), e.Chunks.GetChunkSizes())
	default:
		return errors.New("malformed tree manifest: empty entry")
	}
}

func (a *deltaAssembler) dir(d *pb.TreeManifestDir) error {
	a.files.seal()
	node := &treedelta.Dir{Name: d.GetName(), Unchanged: d.GetUnchanged()}
	depth := int(d.GetDepth())
	switch {
	case depth == 0 && a.root == nil:
		if raw := d.GetTreeHash(); len(raw) != 0 {
			var hash serverDomain.Hash
			if len(raw) != len(hash) {
				return fmt.Errorf("malformed tree manifest: hash has %d bytes", len(raw))
			}
			copy(hash[:], raw)
			a.hash = &hash
		}
		a.root = node
	case depth >= 1 && depth <= len(a.stack):
		if err := a.checkName(a.stack[1:depth], d.GetName()); err != nil {
			return err
		}
		a.stack = a.stack[:depth]
		parent := a.stack[depth-1]
		if parent.Unchanged {
			return fmt.Errorf("malformed tree manifest: directory %q inside an unchanged directory", d.GetName())
		}
		parent.Dirs = append(parent.Dirs, node)
	default:
		return fmt.Errorf("malformed tree manifest: directory %q at depth %d", d.GetName(), depth)
	}
	a.stack = append(a.stack, node)
	return nil
}

func (a *deltaAssembler) file(f *pb.TreeManifestFile) error {
	a.files.seal()
	if len(a.stack) == 0 {
		return fmt.Errorf("malformed tree manifest: file %q before any directory", f.GetName())
	}
	if err := a.checkName(a.stack[1:], f.GetName()); err != nil {
		return err
	}
	dir := a.stack[len(a.stack)-1]
	if dir.Unchanged {
		return fmt.Errorf("malformed tree manifest: file %q inside an unchanged directory", f.GetName())
	}
	if f.GetUnchanged() {
		if len(f.GetChunkHashes()) != 0 {
			return fmt.Errorf("malformed tree manifest: unchanged file %q with chunks", f.GetName())
		}
		dir.Files = append(dir.Files, treedelta.File{Name: f.GetName()})
		return nil
	}
	node := &serverDomain.File{
		Name:      f.GetName(),
		Mode:      int(f.GetMode()),
		SizeBytes: f.GetSizeBytes(),
		IsBinary:  f.GetIsBinary(),
		Encoding:  f.GetEncoding(),
	}
	dir.Files = append(dir.Files, treedelta.File{Name: node.Name, Changed: node})
	a.files.last, a.files.sized = node, true
	return a.files.chunks(f.GetChunkHashes(), f.GetChunkSizes())
}

func (a *deltaAssembler) finish() (*treedelta.Delta, error) {
	a.files.seal()
	if a.root == nil {
		return nil, errors.New("malformed tree manifest: a delta without a root directory")
	}
	return &treedelta.Delta{Root: a.root, RootHash: a.hash}, nil
}

func (a *deltaAssembler) checkName(parents []*treedelta.Dir, name string) error {
	if err := serverDomain.ValidateLocalPathSegment(name); err != nil {
		path := name
		for i := len(parents) - 1; i >= 0; i-- {
			path = parents[i].Name + "/" + path
		}
		return unsafeManifestName(path, err)
	}
	return nil
}

// treeAssembler builds the domain tree from manifest entries with the same
// rules as manifestAssembler: names are checked, hashes must be whole.
type treeAssembler struct {
	root  *serverDomain.TreeNode
	stack []*serverDomain.TreeNode
	last  *serverDomain.File
	// sized is false once a part of the current file came without one size
	// per hash; the file then carries no chunk sizes, like an older server's.
	sized bool
}

func (a *treeAssembler) add(entry *pb.TreeManifestEntry) error {
	switch e := entry.GetEntry().(type) {
	case *pb.TreeManifestEntry_Dir:
		return a.dir(e.Dir)
	case *pb.TreeManifestEntry_File:
		return a.file(e.File)
	case *pb.TreeManifestEntry_Chunks:
		if a.last == nil {
			return errors.New("malformed tree manifest: chunks before any file")
		}
		return a.chunks(e.Chunks.GetChunkHashes(), e.Chunks.GetChunkSizes())
	default:
		return errors.New("malformed tree manifest: empty entry")
	}
}

func (a *treeAssembler) dir(d *pb.TreeManifestDir) error {
	a.seal()
	var hash serverDomain.Hash
	if len(d.GetTreeHash()) != len(hash) {
		return fmt.Errorf("malformed tree manifest: hash has %d bytes", len(d.GetTreeHash()))
	}
	copy(hash[:], d.GetTreeHash())
	node := &serverDomain.TreeNode{Name: d.GetName(), Hash: hash, TreeChildren: []*serverDomain.TreeNode{}, FileChildren: []*serverDomain.File{}}
	depth := int(d.GetDepth())
	switch {
	case depth == 0 && a.root == nil:
		a.root = node
	case depth >= 1 && depth <= len(a.stack):
		if err := a.checkName(a.stack[1:depth], d.GetName()); err != nil {
			return err
		}
		a.stack = a.stack[:depth]
		parent := a.stack[depth-1]
		parent.TreeChildren = append(parent.TreeChildren, node)
	default:
		return fmt.Errorf("malformed tree manifest: directory %q at depth %d", d.GetName(), depth)
	}
	a.stack = append(a.stack, node)
	return nil
}

func (a *treeAssembler) file(f *pb.TreeManifestFile) error {
	a.seal()
	if len(a.stack) == 0 {
		return fmt.Errorf("malformed tree manifest: file %q before any directory", f.GetName())
	}
	if err := a.checkName(a.stack[1:], f.GetName()); err != nil {
		return err
	}
	node := &serverDomain.File{
		Name:      f.GetName(),
		Mode:      int(f.GetMode()),
		SizeBytes: f.GetSizeBytes(),
		IsBinary:  f.GetIsBinary(),
		Encoding:  f.GetEncoding(),
	}
	dir := a.stack[len(a.stack)-1]
	dir.FileChildren = append(dir.FileChildren, node)
	a.last, a.sized = node, true
	return a.chunks(f.GetChunkHashes(), f.GetChunkSizes())
}

func (a *treeAssembler) chunks(hashes [][]byte, sizes []int64) error {
	if len(sizes) != len(hashes) {
		a.sized = false
	}
	if a.last.Chunks == nil && len(hashes) > 0 {
		a.last.Chunks = make([]serverDomain.Chunk, 0, len(hashes))
	}
	for i, raw := range hashes {
		chunk := serverDomain.Chunk{}
		if len(raw) != len(chunk.Hash) {
			return fmt.Errorf("malformed tree manifest: hash has %d bytes", len(raw))
		}
		copy(chunk.Hash[:], raw)
		if a.sized {
			chunk.SizeBytes = sizes[i]
		}
		a.last.Chunks = append(a.last.Chunks, chunk)
	}
	return nil
}

// seal finishes the file being read: its hash covers all its chunks, and its
// chunk sizes are dropped unless every part supplied them.
func (a *treeAssembler) seal() {
	f := a.last
	if f == nil {
		return
	}
	a.last = nil
	hasher := chunker.NewHasher()
	for i := range f.Chunks {
		_, _ = hasher.Write(f.Chunks[i].Hash[:])
		if !a.sized {
			f.Chunks[i].SizeBytes = 0
		}
	}
	f.Hash = hasher.Sum()
}

func (a *treeAssembler) finish() *serverDomain.TreeNode {
	a.seal()
	return a.root
}

func (a *treeAssembler) checkName(parents []*serverDomain.TreeNode, name string) error {
	if err := serverDomain.ValidateLocalPathSegment(name); err != nil {
		path := name
		for i := len(parents) - 1; i >= 0; i-- {
			path = parents[i].Name + "/" + path
		}
		return unsafeManifestName(path, err)
	}
	return nil
}

type manifestAssembler struct {
	root  *pb.TreeManifest
	stack []*pb.TreeManifest
	last  *pb.FileNode
}

func (a *manifestAssembler) add(entry *pb.TreeManifestEntry) error {
	switch e := entry.GetEntry().(type) {
	case *pb.TreeManifestEntry_Dir:
		return a.dir(e.Dir)
	case *pb.TreeManifestEntry_File:
		return a.file(e.File)
	case *pb.TreeManifestEntry_Chunks:
		if a.last == nil {
			return errors.New("malformed tree manifest: chunks before any file")
		}
		hashes, err := hexHashes(e.Chunks.GetChunkHashes())
		if err != nil {
			return err
		}
		a.last.ChunkHashes = append(a.last.ChunkHashes, hashes...)
		a.last.ChunkSizes = append(a.last.ChunkSizes, e.Chunks.GetChunkSizes()...)
		return nil
	default:
		return errors.New("malformed tree manifest: empty entry")
	}
}

func (a *manifestAssembler) dir(d *pb.TreeManifestDir) error {
	hash, err := hexHash(d.GetTreeHash())
	if err != nil {
		return err
	}
	node := &pb.TreeManifest{TreeHash: hash, Path: d.GetName()}
	depth := int(d.GetDepth())
	if depth >= 1 && depth <= len(a.stack) {
		if err := checkManifestName(a.stack[1:depth], d.GetName()); err != nil {
			return err
		}
	}
	switch {
	case depth == 0 && a.root == nil:
		a.root = node
	case depth >= 1 && depth <= len(a.stack):
		a.stack = a.stack[:depth]
		parent := a.stack[depth-1]
		parent.SubTrees = append(parent.SubTrees, node)
	default:
		return fmt.Errorf("malformed tree manifest: directory %q at depth %d", d.GetName(), depth)
	}
	a.stack = append(a.stack, node)
	a.last = nil
	return nil
}

func (a *manifestAssembler) file(f *pb.TreeManifestFile) error {
	if len(a.stack) == 0 {
		return fmt.Errorf("malformed tree manifest: file %q before any directory", f.GetName())
	}
	if err := checkManifestName(a.stack[1:], f.GetName()); err != nil {
		return err
	}
	hashes, err := hexHashes(f.GetChunkHashes())
	if err != nil {
		return err
	}
	node := &pb.FileNode{
		Path:        f.GetName(),
		Mode:        f.GetMode(),
		SizeBytes:   f.GetSizeBytes(),
		IsBinary:    f.GetIsBinary(),
		Encoding:    f.GetEncoding(),
		ChunkHashes: hashes,
		ChunkSizes:  f.GetChunkSizes(),
	}
	dir := a.stack[len(a.stack)-1]
	dir.Files = append(dir.Files, node)
	a.last = node
	return nil
}

// checkManifestName rejects a directory or file name that is not a single safe
// path segment for this machine, so a hostile manifest cannot place files
// outside the working copy or inside its .nipa metadata. parents are the
// enclosing directories below the root, whose name is only a label.
func checkManifestName(parents []*pb.TreeManifest, name string) error {
	if err := serverDomain.ValidateLocalPathSegment(name); err != nil {
		path := name
		for i := len(parents) - 1; i >= 0; i-- {
			path = parents[i].GetPath() + "/" + path
		}
		return unsafeManifestName(path, err)
	}
	return nil
}

// validateManifestNames applies checkManifestName to a whole tree that did not
// come through the streaming assembler.
func validateManifestNames(root *pb.TreeManifest) error {
	var walk func(node *pb.TreeManifest, parents []*pb.TreeManifest) error
	walk = func(node *pb.TreeManifest, parents []*pb.TreeManifest) error {
		for _, f := range node.GetFiles() {
			if err := checkManifestName(parents, f.GetPath()); err != nil {
				return err
			}
		}
		for _, sub := range node.GetSubTrees() {
			if err := checkManifestName(parents, sub.GetPath()); err != nil {
				return err
			}
			if err := walk(sub, append(parents[:len(parents):len(parents)], sub)); err != nil {
				return err
			}
		}
		return nil
	}
	if root == nil {
		return nil
	}
	return walk(root, nil)
}

func unsafeManifestName(path string, err error) error {
	return &domain.Error{
		Code:    400,
		Message: fmt.Sprintf("the server sent an unsafe path %q in the tree manifest: %s", path, err.Error()),
		Hint:    "the path cannot be written safely inside the working copy; nothing was written",
	}
}

func hexHashes(raw [][]byte) ([]string, error) {
	out := make([]string, len(raw))
	for i, h := range raw {
		s, err := hexHash(h)
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

func hexHash(raw []byte) (string, error) {
	if len(raw) != len(serverDomain.Hash{}) {
		return "", fmt.Errorf("malformed tree manifest: hash has %d bytes", len(raw))
	}
	return hex.EncodeToString(raw), nil
}
