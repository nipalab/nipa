package server

import (
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	manifestPartBudget = 1 << 20
	// manifestChunkBytes is the encoded size of one chunk: a 32-byte hash
	// with its tag and length plus a packed size of at most ten bytes.
	manifestChunkBytes     = 44
	maxManifestChunksEntry = (manifestPartBudget - 4096) / manifestChunkBytes
)

// StreamTreeManifest sends GetTreeManifest's tree in parts of about
// manifestPartBudget bytes. A recursive request for a whole commit also gets
// the commit detail, so one call replaces GetCommit.
func (n *nipaServer) StreamTreeManifest(req *pb.GetTreeManifestRequest, stream grpc.ServerStreamingServer[pb.TreeManifestPart]) error {
	ctx := stream.Context()
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return handleError(err)
	}
	first := &pb.TreeManifestPart{Branch: req.GetBranch()}
	var root *domain.TreeNode
	if wholeCommit(req) {
		id, perr := snow.ParseBase36(req.GetCommitId())
		if perr != nil {
			return handleError(domain.NewErrorUser("invalid commit id"))
		}
		var commit *domain.Commit
		commit, root, err = n.uc.Branch().GetCommit(ctx, project.ID, id)
		if err == nil {
			first.Commit = domainCommitToDetailPB(commit, root)
		}
	} else {
		root, err = n.manifestRoot(ctx, project.ID, req)
	}
	if err != nil {
		return handleError(err)
	}
	w := &manifestWriter{send: stream.Send, part: first}
	if err := w.dir(root, 0); err != nil {
		return err
	}
	return w.flush(true)
}

func wholeCommit(req *pb.GetTreeManifestRequest) bool {
	return req.GetCommitId() != "" && req.GetRecursive() && len(req.GetPaths()) == 0 && req.GetPath() == ""
}

type manifestWriter struct {
	send func(*pb.TreeManifestPart) error
	part *pb.TreeManifestPart
	size int
	sent bool
}

func (w *manifestWriter) add(entry *pb.TreeManifestEntry) error {
	n := proto.Size(entry) + 8
	if w.size+n > manifestPartBudget {
		if err := w.flush(false); err != nil {
			return err
		}
	}
	w.part.Entries = append(w.part.Entries, entry)
	w.size += n
	return nil
}

func (w *manifestWriter) flush(final bool) error {
	if len(w.part.Entries) == 0 && (w.sent || !final) {
		return nil
	}
	if err := w.send(w.part); err != nil {
		return err
	}
	w.part, w.size, w.sent = &pb.TreeManifestPart{}, 0, true
	return nil
}

func (w *manifestWriter) dir(node *domain.TreeNode, depth int32) error {
	if node == nil {
		return nil
	}
	err := w.add(&pb.TreeManifestEntry{Entry: &pb.TreeManifestEntry_Dir{Dir: &pb.TreeManifestDir{
		Depth: depth, Name: node.Name, TreeHash: append([]byte(nil), node.Hash[:]...),
	}}})
	if err != nil {
		return err
	}
	for _, file := range node.FileChildren {
		if err := w.file(file); err != nil {
			return err
		}
	}
	for _, child := range node.TreeChildren {
		if err := w.dir(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (w *manifestWriter) file(file *domain.File) error {
	hashes := make([][]byte, len(file.Chunks))
	sizes := make([]int64, 0, len(file.Chunks))
	for i, chunk := range file.Chunks {
		hashes[i] = append([]byte(nil), chunk.Hash[:]...)
		if chunk.SizeBytes > 0 && sizes != nil {
			sizes = append(sizes, chunk.SizeBytes)
		} else {
			sizes = nil
		}
	}
	head := min(len(hashes), maxManifestChunksEntry)
	entry := &pb.TreeManifestFile{
		Name:        file.Name,
		Mode:        domainFileModeToPB(file.Mode),
		SizeBytes:   file.SizeBytes,
		IsBinary:    file.IsBinary,
		Encoding:    file.Encoding,
		ChunkHashes: hashes[:head],
	}
	if len(sizes) > 0 {
		entry.ChunkSizes = sizes[:head]
	}
	if err := w.add(&pb.TreeManifestEntry{Entry: &pb.TreeManifestEntry_File{File: entry}}); err != nil {
		return err
	}
	for start := head; start < len(hashes); start += maxManifestChunksEntry {
		end := min(start+maxManifestChunksEntry, len(hashes))
		more := &pb.TreeManifestChunks{ChunkHashes: hashes[start:end]}
		if len(sizes) > 0 {
			more.ChunkSizes = sizes[start:end]
		}
		if err := w.add(&pb.TreeManifestEntry{Entry: &pb.TreeManifestEntry_Chunks{Chunks: more}}); err != nil {
			return err
		}
	}
	return nil
}
