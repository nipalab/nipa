package server

import (
	"context"
	"errors"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (n *nipaServer) ImportLookup(ctx context.Context, req *pb.ImportLookupRequest) (*pb.ImportLookupResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return nil, handleError(err)
	}
	found, err := n.uc.Import().Lookup(ctx, project.ID, req.GetOrigins())
	if err != nil {
		return nil, handleError(err)
	}
	res := &pb.ImportLookupResponse{Commits: make([]*pb.ImportedCommit, 0, len(found))}
	for _, origin := range req.GetOrigins() {
		if c, ok := found[origin]; ok {
			res.Commits = append(res.Commits, &pb.ImportedCommit{Origin: c.Origin, CommitId: c.CommitID.Base36(), CommitHash: c.CommitHash.String()})
			delete(found, origin)
		}
	}
	return res, nil
}

func (n *nipaServer) ImportSetRefs(ctx context.Context, req *pb.ImportSetRefsRequest) (*pb.ImportSetRefsResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return nil, handleError(err)
	}
	results, err := n.uc.Import().SetRefs(ctx, project.ID, usecase.SetRefsInput{
		Branches: importRefs(req.GetBranches()),
		Tags:     importRefs(req.GetTags()),
		Default:  req.GetDefaultBranch(),
		Update:   req.GetUpdate(),
		Commits:  int(req.GetCommitCount()),
	})
	if err != nil {
		return nil, handleError(err)
	}
	res := &pb.ImportSetRefsResponse{Refs: make([]*pb.ImportRefResult, 0, len(results))}
	for _, r := range results {
		out := &pb.ImportRefResult{Kind: r.Kind, Name: r.Name, Status: r.Status, Message: r.Message}
		if r.CommitID != 0 {
			out.CommitId = r.CommitID.Base36()
		}
		res.Refs = append(res.Refs, out)
	}
	return res, nil
}

func importRefs(refs []*pb.ImportRef) []usecase.ImportRef {
	out := make([]usecase.ImportRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, usecase.ImportRef{Name: r.GetName(), Origin: r.GetOrigin(), Message: r.GetMessage()})
	}
	return out
}

// ImportStream applies one commit per header/parts/end sequence and answers
// each with its stored commit. Any error ends the stream.
func (n *nipaServer) ImportStream(stream grpc.BidiStreamingServer[pb.ImportStreamRequest, pb.ImportStreamResponse]) error {
	ctx := stream.Context()
	imp := n.uc.Import()
	var projectID snow.ID
	var header *pb.ImportCommitHeader
	var recv *pushReceiver
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if header != nil {
				return handleError(domain.NewErrorUser("import stream ended inside a commit"))
			}
			return nil
		}
		if err != nil {
			return err
		}
		switch part := msg.GetPart().(type) {
		case *pb.ImportStreamRequest_Header:
			if header != nil {
				return handleError(domain.NewErrorUser("import stream sent a header before ending the previous commit"))
			}
			if projectID == 0 {
				if projectID, err = n.importProject(ctx, part.Header.GetContext()); err != nil {
					return err
				}
			}
			header = part.Header
			recv = &pushReceiver{maxFiles: imp.MaxFiles(), maxBytes: maxPushStreamBytes, tooMany: imp.TooManyFiles, size: proto.Size(msg)}
		case *pb.ImportStreamRequest_End:
			if header == nil {
				return handleError(domain.NewErrorUser("import stream ended a commit it never started"))
			}
			if err := n.applyImportCommit(stream, projectID, header, recv); err != nil {
				return err
			}
			header, recv = nil, nil
		default:
			if err := addImportPart(header, recv, msg); err != nil {
				return handleError(err)
			}
		}
	}
}

func (n *nipaServer) importProject(ctx context.Context, pc *pb.ProjectContext) (snow.ID, error) {
	if pc == nil {
		return 0, handleError(domain.NewErrorUser("the first import header must name the project"))
	}
	_, project, err := n.uc.Common().ResolveBySlug(ctx, pc.GetOrg(), pc.GetProject())
	if err != nil {
		return 0, handleError(err)
	}
	if err := n.uc.Import().CheckAccess(ctx, project.ID); err != nil {
		return 0, handleError(err)
	}
	return project.ID, nil
}

func addImportPart(header *pb.ImportCommitHeader, recv *pushReceiver, msg *pb.ImportStreamRequest) error {
	if header == nil {
		return domain.NewErrorUser("import stream must start each commit with a header")
	}
	if err := recv.grow(proto.Size(msg)); err != nil {
		return err
	}
	switch part := msg.GetPart().(type) {
	case *pb.ImportStreamRequest_Files:
		return recv.addFiles(part.Files)
	case *pb.ImportStreamRequest_Chunks:
		return recv.addChunks(part.Chunks)
	case *pb.ImportStreamRequest_Removed:
		return recv.addRemoved(part.Removed)
	default:
		return domain.NewErrorUser("import stream sent an empty message")
	}
}

func (n *nipaServer) applyImportCommit(stream grpc.BidiStreamingServer[pb.ImportStreamRequest, pb.ImportStreamResponse], projectID snow.ID, header *pb.ImportCommitHeader, recv *pushReceiver) error {
	in := usecase.ImportCommit{
		Origin:        header.GetOrigin(),
		ParentOrigins: header.GetParentOrigins(),
		Message:       header.GetMessage(),
		AuthorName:    header.GetAuthorName(),
		AuthorEmail:   header.GetAuthorEmail(),
		Files:         recv.files,
		Removed:       recv.removed,
	}
	if at := header.GetAuthoredAt(); at != nil {
		in.AuthoredAt = at.AsTime().In(time.UTC)
	}
	if raw := header.GetAuthorUserId(); raw != "" {
		id, err := snow.ParseBase36(raw)
		if err != nil {
			return handleError(domain.NewErrorUser("invalid author user id " + raw))
		}
		in.AuthorUserID = id
	}
	result, err := n.uc.Import().Commit(stream.Context(), projectID, in)
	if err != nil {
		return handleError(err)
	}
	return stream.Send(&pb.ImportStreamResponse{
		Origin:     result.Origin,
		CommitId:   result.CommitID.Base36(),
		CommitHash: result.CommitHash.String(),
	})
}
