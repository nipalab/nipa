package grpc

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
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

func (c *Client) treeManifest(ctx context.Context, req *pb.GetTreeManifestRequest) (*serverDomain.TreeNode, error) {
	res, _, err := c.StreamTreeManifest(ctx, req)
	if err != nil {
		return nil, streamError(err, "streamed tree manifests")
	}
	return toServerTreeNode(res.GetRootTree()), nil
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.StreamTreeManifest(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	res := &pb.GetTreeManifestResponse{}
	var commit *pb.CommitDetail
	var a manifestAssembler
	for first := true; ; first = false {
		part, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		if first {
			res.Branch = part.GetBranch()
			commit = part.GetCommit()
		}
		for _, entry := range part.GetEntries() {
			if err := a.add(entry); err != nil {
				return nil, nil, err
			}
		}
	}
	res.RootTree = a.root
	return res, commit, nil
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
