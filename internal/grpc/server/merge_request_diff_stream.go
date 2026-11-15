package server

import (
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/nipalab/nipa/internal/grpc/pb"
)

const (
	diffPartBudget = 1 << 20
	maxDiffFile    = diffPartBudget - 4096
)

// StreamMergeRequestDiff sends GetMergeRequestDiff's files in parts of about
// diffPartBudget bytes. A file whose patch and hunks do not fit one part is
// sent without the hunks, and without the patch too (marked too_large) when
// the patch alone still does not fit.
func (n *nipaServer) StreamMergeRequestDiff(req *pb.GetMergeRequestDiffRequest, stream grpc.ServerStreamingServer[pb.MergeRequestDiffPart]) error {
	ctx := stream.Context()
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return handleError(err)
	}
	files, err := n.uc.MergeRequest().Diff(ctx, project.ID, req.GetNumber())
	if err != nil {
		return handleError(err)
	}
	part := &pb.MergeRequestDiffPart{}
	size := 0
	for _, file := range files {
		detail := diffFileToPB(file)
		n := proto.Size(detail)
		if n > maxDiffFile {
			detail.Hunks = nil
			n = proto.Size(detail)
		}
		if n > maxDiffFile {
			detail.Patch, detail.TooLarge = nil, true
			n = proto.Size(detail)
		}
		n += 8
		if size+n > diffPartBudget && len(part.Files) > 0 {
			if err := stream.Send(part); err != nil {
				return err
			}
			part, size = &pb.MergeRequestDiffPart{}, 0
		}
		part.Files = append(part.Files, detail)
		size += n
	}
	if len(part.Files) == 0 {
		return nil
	}
	return stream.Send(part)
}
