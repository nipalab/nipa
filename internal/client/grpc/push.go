package grpc

import (
	"context"
	"errors"
	"io"

	"google.golang.org/protobuf/proto"

	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	pushPartBudget = 1 << 20
	// hashFieldBytes is the encoded size of one 32-byte hash in a repeated
	// bytes field: tag, length and the hash.
	hashFieldBytes   = 34
	maxHashesPerPart = (pushPartBudget - 4096) / hashFieldBytes
)

// Push streams the push manifest in parts of about pushPartBudget bytes, so
// the file list is never bound by the gRPC message size limit.
func (c *Client) Push(ctx context.Context, org, project, branch, baseTreeHash, message string, files []*serverDomain.PushFile, removed []string, parent2CommitHash, baseCommitID string) (*serverDomain.PushResult, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	header := &pb.PushStreamHeader{
		Context:      &pb.ProjectContext{Org: org, Project: project},
		Branch:       branch,
		BaseTreeHash: baseTreeHash,
		Message:      message,
	}
	if parent2CommitHash != "" {
		header.Parent_2CommitHash = &parent2CommitHash
	}
	if baseCommitID != "" {
		header.BaseCommitId = &baseCommitID
	}

	var res *pb.PushResponse
	err = c.invokeWithThrottleRetry(ctx, func() error {
		return c.callAuthed(ctx, func(authCtx context.Context) error {
			var err error
			res, err = pushOnce(authCtx, client, header, files, removed)
			return err
		})
	})
	if err != nil {
		return nil, streamError(err, "streamed pushes")
	}
	commitID, _ := snow.ParseBase36(res.GetCommitId())
	commitHash, err := decodeHash(res.GetCommitHash())
	if err != nil {
		return nil, err
	}
	treeHash, err := decodeHash(res.GetTreeHash())
	if err != nil {
		return nil, err
	}
	return &serverDomain.PushResult{
		CommitID:   commitID,
		CommitHash: commitHash,
		TreeHash:   treeHash,
		View: serverDomain.PushView{
			Token:        res.GetView(),
			Unrestricted: res.GetViewUnrestricted(),
			HidesPushed:  res.GetViewHidesPushed(),
		},
	}, nil
}

func pushOnce(ctx context.Context, client pb.NipaServiceClient, header *pb.PushStreamHeader, files []*serverDomain.PushFile, removed []string) (*pb.PushResponse, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.PushStream(ctx)
	if err != nil {
		return nil, err
	}
	err = sendPushParts(stream.Send, header, files, removed)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	// io.EOF from Send means the server ended the call; CloseAndRecv
	// returns its status.
	return stream.CloseAndRecv()
}

func sendPushParts(send func(*pb.PushStreamRequest) error, header *pb.PushStreamHeader, files []*serverDomain.PushFile, removed []string) error {
	if err := send(&pb.PushStreamRequest{Part: &pb.PushStreamRequest_Header{Header: header}}); err != nil {
		return err
	}
	if err := sendPushFiles(send, files); err != nil {
		return err
	}
	return sendPushRemoved(send, removed)
}

func sendPushFiles(send func(*pb.PushStreamRequest) error, files []*serverDomain.PushFile) error {
	var batch []*pb.PushStreamFile
	size := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := send(&pb.PushStreamRequest{Part: &pb.PushStreamRequest_Files{Files: &pb.PushStreamFiles{Files: batch}}})
		batch, size = nil, 0
		return err
	}
	for _, f := range files {
		pf := toPushStreamFile(f)
		var rest [][]byte
		if len(pf.ChunkHashes) > maxHashesPerPart {
			rest = pf.ChunkHashes[maxHashesPerPart:]
			pf.ChunkHashes = pf.ChunkHashes[:maxHashesPerPart]
		}
		n := proto.Size(pf) + 8
		if size+n > pushPartBudget {
			if err := flush(); err != nil {
				return err
			}
		}
		batch = append(batch, pf)
		size += n
		if len(rest) == 0 {
			continue
		}
		if err := flush(); err != nil {
			return err
		}
		for len(rest) > 0 {
			k := min(len(rest), maxHashesPerPart)
			if err := send(&pb.PushStreamRequest{Part: &pb.PushStreamRequest_Chunks{Chunks: &pb.PushStreamChunks{ChunkHashes: rest[:k]}}}); err != nil {
				return err
			}
			rest = rest[k:]
		}
	}
	return flush()
}

func sendPushRemoved(send func(*pb.PushStreamRequest) error, removed []string) error {
	var batch []string
	size := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := send(&pb.PushStreamRequest{Part: &pb.PushStreamRequest_Removed{Removed: &pb.PushStreamRemoved{Paths: batch}}})
		batch, size = nil, 0
		return err
	}
	for _, p := range removed {
		n := len(p) + 8
		if size+n > pushPartBudget {
			if err := flush(); err != nil {
				return err
			}
		}
		batch = append(batch, p)
		size += n
	}
	return flush()
}

func toPushStreamFile(f *serverDomain.PushFile) *pb.PushStreamFile {
	pf := &pb.PushStreamFile{
		Path:        f.Path,
		Mode:        intToPBFileMode(f.Mode),
		SizeBytes:   f.SizeBytes,
		IsBinary:    f.IsBinary,
		Encoding:    f.Encoding,
		FileHash:    append([]byte(nil), f.FileHash[:]...),
		ChunkHashes: make([][]byte, len(f.ChunkHashes)),
	}
	for i, h := range f.ChunkHashes {
		pf.ChunkHashes[i] = append([]byte(nil), h[:]...)
	}
	return pf
}
