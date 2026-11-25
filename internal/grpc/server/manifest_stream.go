package server

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treedelta"
	"github.com/nipalab/nipa/internal/usecase"
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
// the commit detail, so one call replaces GetCommit. The first part always
// carries the view token of the manifest; a request naming the manifest the
// client holds (base_commit_id, base_view) is answered with a delta against it
// when the usecase can build one, and with the full manifest otherwise.
func (n *nipaServer) StreamTreeManifest(req *pb.GetTreeManifestRequest, stream grpc.ServerStreamingServer[pb.TreeManifestPart]) error {
	ctx := stream.Context()
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return handleError(err)
	}
	first := &pb.TreeManifestPart{Branch: req.GetBranch()}
	delta, err := n.manifestDelta(ctx, project.ID, req)
	if err != nil {
		return handleError(err)
	}
	if delta != nil {
		return sendManifestDelta(req, first, delta, stream.Send)
	}
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
	// The token only matters to clients that ask for deltas later; a manifest
	// that was built is not failed over it.
	if view, verr := n.uc.Branch().ManifestView(ctx, project.ID, manifestPaths(req), req.GetRecursive()); verr == nil {
		first.View = view
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

// manifestDelta asks the usecase for a delta when the request names a base
// manifest. Nil means a full manifest must be sent: nothing was asked, the
// request is not one a delta exists for, or the usecase declined. Whatever is
// wrong with the request itself is left for the full manifest to report.
func (n *nipaServer) manifestDelta(ctx context.Context, projectID snow.ID, req *pb.GetTreeManifestRequest) (*usecase.ManifestDelta, error) {
	if req.GetBaseCommitId() == "" || !req.GetRecursive() {
		return nil, nil
	}
	base := usecase.ManifestBase{View: req.GetBaseView()}
	var err error
	if base.CommitID, err = snow.ParseBase36(req.GetBaseCommitId()); err != nil {
		return nil, nil
	}
	if raw := req.GetBaseTreeHash(); len(raw) == len(domain.Hash{}) {
		var hash domain.Hash
		copy(hash[:], raw)
		base.TreeHash = &hash
	}
	target := usecase.ManifestTarget{Branch: req.GetBranch()}
	if raw := req.GetCommitId(); raw != "" {
		id, err := snow.ParseBase36(raw)
		if err != nil {
			return nil, nil
		}
		target.CommitID = &id
	}
	return n.uc.Branch().GetTreeManifestDelta(ctx, projectID, target, manifestPaths(req), base)
}

// sendManifestDelta streams a delta. Its first part names the commit it leads
// to and, for a request of a whole commit, carries the commit detail like the
// full manifest does; the detail's tree hash is only set when the delta has a
// root hash, the one hash of a delta the server vouches for.
func sendManifestDelta(req *pb.GetTreeManifestRequest, first *pb.TreeManifestPart, delta *usecase.ManifestDelta, send func(*pb.TreeManifestPart) error) error {
	first.Delta = true
	first.View = req.GetBaseView()
	first.TargetCommitId = delta.Commit.ID.Base36()
	var rootHash []byte
	if hash := delta.Delta.RootHash; hash != nil {
		rootHash = append([]byte(nil), hash[:]...)
	}
	if wholeCommit(req) {
		first.Commit = domainCommitToDetailPB(delta.Commit, nil)
		if hash := delta.Delta.RootHash; hash != nil {
			first.Commit.TreeHash = hash.String()
		}
	}
	w := &manifestWriter{send: send, part: first}
	if err := w.deltaDir(delta.Delta.Root, 0, rootHash); err != nil {
		return err
	}
	return w.flush(true)
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

// deltaDir writes one directory of a delta: an unchanged directory is a
// single entry, a changed one is followed by all its files (unchanged ones by
// name only) and then by its subdirectories. Only the root carries a hash.
func (w *manifestWriter) deltaDir(dir *treedelta.Dir, depth int32, hash []byte) error {
	err := w.add(&pb.TreeManifestEntry{Entry: &pb.TreeManifestEntry_Dir{Dir: &pb.TreeManifestDir{
		Depth: depth, Name: dir.Name, TreeHash: hash, Unchanged: dir.Unchanged,
	}}})
	if err != nil || dir.Unchanged {
		return err
	}
	for _, file := range dir.Files {
		if file.Changed != nil {
			err = w.file(file.Changed)
		} else {
			err = w.add(&pb.TreeManifestEntry{Entry: &pb.TreeManifestEntry_File{File: &pb.TreeManifestFile{Name: file.Name, Unchanged: true}}})
		}
		if err != nil {
			return err
		}
	}
	for _, child := range dir.Dirs {
		if err := w.deltaDir(child, depth+1, nil); err != nil {
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
