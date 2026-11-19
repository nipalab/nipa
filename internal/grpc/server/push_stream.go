package server

import (
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

// maxPushStreamBytes bounds the encoded size of one streamed push manifest
// held in memory, independent of the file count limit.
const maxPushStreamBytes = 1 << 30

func (n *nipaServer) PushStream(stream grpc.ClientStreamingServer[pb.PushStreamRequest, pb.PushResponse]) error {
	ctx := stream.Context()
	first, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return handleError(domain.NewErrorUser("push stream is empty"))
	}
	if err != nil {
		return err
	}
	header := first.GetHeader()
	if header == nil {
		return handleError(domain.NewErrorUser("push stream must start with a header"))
	}
	if header.GetMessage() == "" {
		return handleError(domain.NewErrorUser("commit message is required"))
	}
	_, project, err := n.uc.Common().ResolveBySlug(ctx, header.GetContext().GetOrg(), header.GetContext().GetProject())
	if err != nil {
		return handleError(err)
	}
	push := n.uc.Push()
	if err := push.CheckTarget(ctx, project.ID, header.GetBranch(), header.GetMessage(), header.GetBaseCommitId()); err != nil {
		return handleError(err)
	}

	recv := pushReceiver{maxFiles: push.MaxFiles(), maxBytes: maxPushStreamBytes, tooMany: push.TooManyFiles, size: proto.Size(first)}
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if err := recv.add(msg); err != nil {
			return handleError(err)
		}
	}

	result, err := push.Push(ctx, project.ID, header.GetBranch(), header.GetBaseTreeHash(), header.GetMessage(), recv.files, recv.removed, header.GetParent_2CommitHash(), header.GetBaseCommitId())
	if err != nil {
		return handleError(err)
	}
	return stream.SendAndClose(&pb.PushResponse{
		CommitId:   result.CommitID.Base36(),
		CommitHash: result.CommitHash.String(),
		TreeHash:   result.TreeHash.String(),
	})
}

type pushReceiver struct {
	maxFiles int
	maxBytes int
	tooMany  func(int) error
	size     int
	files    []*domain.PushFile
	removed  []string
}

func (r *pushReceiver) add(msg *pb.PushStreamRequest) error {
	if err := r.grow(proto.Size(msg)); err != nil {
		return err
	}
	switch part := msg.GetPart().(type) {
	case *pb.PushStreamRequest_Header:
		return domain.NewErrorUser("push stream has more than one header")
	case *pb.PushStreamRequest_Files:
		return r.addFiles(part.Files)
	case *pb.PushStreamRequest_Chunks:
		return r.addChunks(part.Chunks)
	case *pb.PushStreamRequest_Removed:
		return r.addRemoved(part.Removed)
	default:
		return domain.NewErrorUser("push stream sent an empty message")
	}
}

func (r *pushReceiver) grow(n int) error {
	r.size += n
	if r.size > r.maxBytes {
		return domain.NewErrorUser(fmt.Sprintf("push manifest is larger than the server limit of %d bytes; push in smaller parts", r.maxBytes))
	}
	return nil
}

func (r *pushReceiver) addFiles(part *pb.PushStreamFiles) error {
	for _, f := range part.GetFiles() {
		file, err := pushStreamFile(f)
		if err != nil {
			return err
		}
		r.files = append(r.files, file)
	}
	return r.checkCount()
}

func (r *pushReceiver) addChunks(part *pb.PushStreamChunks) error {
	if len(r.files) == 0 {
		return domain.NewErrorUser("push stream sent chunk hashes before any file")
	}
	last := r.files[len(r.files)-1]
	for _, h := range part.GetChunkHashes() {
		hash, err := rawHash(h)
		if err != nil {
			return domain.NewErrorUser("invalid chunk hash for " + last.Path)
		}
		last.ChunkHashes = append(last.ChunkHashes, hash)
	}
	return nil
}

func (r *pushReceiver) addRemoved(part *pb.PushStreamRemoved) error {
	r.removed = append(r.removed, part.GetPaths()...)
	return r.checkCount()
}

func (r *pushReceiver) checkCount() error {
	if n := len(r.files) + len(r.removed); n > r.maxFiles {
		return r.tooMany(n)
	}
	return nil
}

func pushStreamFile(f *pb.PushStreamFile) (*domain.PushFile, error) {
	fileHash, err := rawHash(f.GetFileHash())
	if err != nil {
		return nil, domain.NewErrorUser("invalid file hash for " + f.GetPath())
	}
	chunkHashes := make([]domain.Hash, 0, len(f.GetChunkHashes()))
	for _, h := range f.GetChunkHashes() {
		ch, err := rawHash(h)
		if err != nil {
			return nil, domain.NewErrorUser("invalid chunk hash for " + f.GetPath())
		}
		chunkHashes = append(chunkHashes, ch)
	}
	return &domain.PushFile{
		Path:        f.GetPath(),
		Mode:        pbFileModeToInt(f.GetMode()),
		SizeBytes:   f.GetSizeBytes(),
		IsBinary:    f.GetIsBinary(),
		Encoding:    f.GetEncoding(),
		FileHash:    fileHash,
		ChunkHashes: chunkHashes,
	}, nil
}

func rawHash(b []byte) (domain.Hash, error) {
	var h domain.Hash
	if len(b) != len(h) {
		return h, fmt.Errorf("hash has %d bytes, want %d", len(b), len(h))
	}
	copy(h[:], b)
	return h, nil
}
