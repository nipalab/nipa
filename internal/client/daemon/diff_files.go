package daemon

import (
	"strings"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/preview"
)

// maxFileHunkBytes caps the hunk text sent for one file so a single event
// stays well below the gRPC message limit; larger diffs keep their counts and
// set hunks_omitted.
const maxFileHunkBytes = 1 << 20

var diffStatusNames = map[diff.Status]string{
	diff.Added:    "added",
	diff.Modified: "modified",
	diff.Deleted:  "deleted",
	diff.Renamed:  "renamed",
}

// DiffFiles streams one structured event per changed file, for GUIs that
// render their own diff views and pick an asset viewer by kind.
func (s *Server) DiffFiles(req *daemonpb.DiffRequest, stream daemonpb.NipaDaemon_DiffFilesServer) error {
	ctx := stream.Context()
	files, renderOpts, done, err := s.runDiff(ctx, req)
	if err != nil {
		return stream.Send(diffFileFailureEvent(err))
	}
	defer done()

	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		event := &daemonpb.DiffFileEvent{Event: &daemonpb.DiffFileEvent_File{File: diffFileToPB(f, renderOpts)}}
		if err := stream.Send(event); err != nil {
			return err
		}
	}
	return nil
}

func diffFileToPB(f diff.FileDiff, opts diff.Options) *daemonpb.DiffFile {
	c := f.Change
	out := &daemonpb.DiffFile{
		Path:               c.Path,
		Status:             diffStatusNames[c.Status],
		Similarity:         int32(c.Similarity),
		ContentUnavailable: f.OldUnavailable || f.NewUnavailable,
	}
	isBinary := c.New.IsBinary
	if c.Status != diff.Added {
		out.Old = diffSideToPB(c.Old)
		isBinary = isBinary || c.Old.IsBinary
	}
	if c.Status != diff.Deleted {
		out.New = diffSideToPB(c.New)
	}
	out.Kind = string(preview.Classify(c.Path, isBinary))

	hunks := diff.FileHunks(f, opts)
	size := 0
	for _, hunk := range hunks {
		for _, line := range hunk.Lines {
			size += len(line.Text)
			switch line.Kind {
			case '+':
				out.Additions++
			case '-':
				out.Deletions++
			}
		}
	}
	if size > maxFileHunkBytes {
		out.HunksOmitted = true
		return out
	}
	out.Hunks = hunksToPB(hunks)
	return out
}

func diffSideToPB(e diff.Entry) *daemonpb.DiffFileSide {
	side := &daemonpb.DiffFileSide{
		Path:      e.Path,
		Mode:      int32(e.Mode),
		SizeBytes: e.SizeBytes,
		IsBinary:  e.IsBinary,
	}
	if e.Hash != (serverDomain.Hash{}) {
		side.Hash = e.Hash.String()
	}
	return side
}

func hunksToPB(hunks []diff.Hunk) []*pb.DiffHunkDetail {
	out := make([]*pb.DiffHunkDetail, 0, len(hunks))
	for _, hunk := range hunks {
		lines := make([]*pb.DiffLineDetail, 0, len(hunk.Lines))
		for _, line := range hunk.Lines {
			text, _ := strings.CutSuffix(line.Text, "\n")
			detail := &pb.DiffLineDetail{Kind: diff.LineKind(line.Kind), Text: text, NoNewline: line.NoNewline}
			if line.Old > 0 {
				old := int64(line.Old)
				detail.OldLine = &old
			}
			if line.New > 0 {
				n := int64(line.New)
				detail.NewLine = &n
			}
			lines = append(lines, detail)
		}
		out = append(out, &pb.DiffHunkDetail{
			OldStart: int64(hunk.OldStart),
			OldLines: int64(hunk.OldLines),
			NewStart: int64(hunk.NewStart),
			NewLines: int64(hunk.NewLines),
			Lines:    lines,
		})
	}
	return out
}

func diffFileFailureEvent(err error) *daemonpb.DiffFileEvent {
	return &daemonpb.DiffFileEvent{Event: &daemonpb.DiffFileEvent_Failure{
		Failure: &daemonpb.OpFailure{Code: failureCode(err), Message: err.Error()},
	}}
}
