package daemon

import (
	"bytes"
	"context"
	"errors"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/preview"
)

func (r *repo) conflicts() *usecase.Conflicts {
	return usecase.NewConflicts(r.localRepo, r.wc, r.root)
}

// Conflicts describes the merge or revert waiting in a clone and its
// conflicted files, with the revisions of each side to compare.
func (s *Server) Conflicts(ctx context.Context, req *daemonpb.ConflictsRequest) (*daemonpb.ConflictsResponse, error) {
	var set *clientDomain.ConflictSet
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		var err error
		set, err = rp.conflicts().List()
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return conflictsToPB(set), nil
}

func conflictsToPB(set *clientDomain.ConflictSet) *daemonpb.ConflictsResponse {
	if set == nil {
		return &daemonpb.ConflictsResponse{}
	}
	rev := func(r clientDomain.ConflictRevision) *daemonpb.ConflictRevision {
		return &daemonpb.ConflictRevision{Id: r.ID, Label: r.Label}
	}
	resp := &daemonpb.ConflictsResponse{Op: set.Op, Base: rev(set.Base), Ours: rev(set.Ours), Theirs: rev(set.Theirs)}
	for _, f := range set.Files {
		resp.Files = append(resp.Files, &daemonpb.ConflictFile{
			Path:     f.Path,
			Kind:     f.Kind,
			Binary:   f.Binary,
			Resolved: f.Resolved,
			Viewer:   string(preview.Classify(f.Path, f.Binary)),
		})
	}
	return resp
}

// ResolveConflict settles one conflicted file: ours or theirs takes that
// side's committed version (a side without the file deletes it), content
// writes the given bytes, working keeps the file on disk and delete removes
// it. The outcome is staged and the refreshed status returned.
func (s *Server) ResolveConflict(ctx context.Context, req *daemonpb.ResolveConflictRequest) (*daemonpb.StatusResponse, error) {
	var st *clientDomain.Status
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		defer rp.changes.publish(nil)
		c := rp.conflicts()
		if err := resolveConflict(ctx, rp, c, req); err != nil {
			return err
		}
		var err error
		st, err = rp.wc.Status(ctx)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return statusToPB(st), nil
}

func resolveConflict(ctx context.Context, rp *repo, c *usecase.Conflicts, req *daemonpb.ResolveConflictRequest) error {
	path := req.GetPath()
	switch req.GetChoice() {
	case "content":
		return c.Resolve(ctx, path, usecase.Resolution{Content: bytes.NewReader(req.GetContent())})
	case "working":
		return c.Resolve(ctx, path, usecase.Resolution{})
	case "delete":
		return c.Resolve(ctx, path, usecase.Resolution{Delete: true})
	case "ours", "theirs":
		set, err := c.List()
		if err != nil {
			return err
		}
		if set == nil {
			return clientDomain.NewUserError("no merge or revert is waiting")
		}
		rev := set.Ours.ID
		if req.GetChoice() == "theirs" {
			rev = set.Theirs.ID
		}
		return takeVersion(ctx, rp, c, path, rev)
	default:
		return status.Errorf(codes.InvalidArgument, "unknown choice %q: use ours, theirs, content, working or delete", req.GetChoice())
	}
}

func takeVersion(ctx context.Context, rp *repo, c *usecase.Conflicts, path, rev string) error {
	if rev == "" {
		return c.Resolve(ctx, path, usecase.Resolution{Delete: true})
	}
	if rp.diff == nil {
		return status.Error(codes.FailedPrecondition, "diff is not configured")
	}
	req := usecase.BlobRequest{Source: usecase.BlobRevision, Revision: rev, Path: path}
	err := rp.diff.ReadBlob(ctx, rp.root, req, func(entry diff.Entry, r io.Reader) error {
		return c.Resolve(ctx, path, usecase.Resolution{Content: r, AllowMarkers: true, Mode: entry.Mode})
	})
	if errors.Is(err, usecase.ErrFileMissing) {
		return c.Resolve(ctx, path, usecase.Resolution{Delete: true})
	}
	return err
}
