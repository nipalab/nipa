package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/godotres"
)

const maxGodotMergeBytes = 64 << 20

var godotMergeExts = []string{".tscn", ".tres"}

// MergeGodotResource merges the base, ours and theirs versions of one
// conflicted .tscn/.tres of the pending merge or revert with
// godotres.Merge3. It only reads: the caller applies a clean result through
// ResolveConflict{choice: content}.
func (s *Server) MergeGodotResource(ctx context.Context, req *daemonpb.MergeGodotResourceRequest) (*daemonpb.MergeGodotResourceResponse, error) {
	p := strings.TrimPrefix(path.Clean("/"+req.GetPath()), "/")
	if p == "" || !slices.Contains(godotMergeExts, strings.ToLower(path.Ext(p))) {
		return nil, toStatusError(clientDomain.NewUserError(fmt.Sprintf("%q is not a Godot text scene or resource (.tscn, .tres)", req.GetPath())))
	}
	var resp *daemonpb.MergeGodotResourceResponse
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		var err error
		resp, err = mergeGodotResource(ctx, rp, p)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return resp, nil
}

func mergeGodotResource(ctx context.Context, rp *repo, p string) (*daemonpb.MergeGodotResourceResponse, error) {
	set, err := rp.conflicts().List()
	if err != nil {
		return nil, err
	}
	if set == nil {
		return nil, clientDomain.NewUserError("no merge or revert is waiting")
	}
	idx := slices.IndexFunc(set.Files, func(f clientDomain.ConflictFile) bool { return f.Path == p })
	if idx < 0 {
		return nil, clientDomain.NewUserError(fmt.Sprintf("%s has no conflict to resolve", p))
	}
	if set.Files[idx].Binary {
		return nil, clientDomain.NewUserError(fmt.Sprintf("%s is stored as binary; pick ours or theirs", p))
	}
	if rp.diff == nil {
		return nil, status.Error(codes.FailedPrecondition, "diff is not configured")
	}
	var sides [3][]byte
	var present [3]bool
	for i, rev := range []string{set.Base.ID, set.Ours.ID, set.Theirs.ID} {
		sides[i], present[i], err = readRevision(ctx, rp, rev, p)
		if err != nil {
			return nil, err
		}
	}
	for i, label := range []string{"base", "ours", "theirs"} {
		if i > 0 && !present[i] {
			return &daemonpb.MergeGodotResourceResponse{Conflicts: []*daemonpb.GodotMergeConflict{{
				Message: label + " deleted the file: pick ours or theirs",
			}}}, nil
		}
	}
	merged, conflicts, err := godotres.Merge3(sides[0], sides[1], sides[2])
	if err != nil {
		return nil, clientDomain.NewUserError(fmt.Sprintf("%s cannot be merged: %v", p, err))
	}
	resp := &daemonpb.MergeGodotResourceResponse{Merged: merged, Clean: len(conflicts) == 0}
	for _, c := range conflicts {
		resp.Conflicts = append(resp.Conflicts, &daemonpb.GodotMergeConflict{Section: c.Section, Key: c.Key, Message: c.Message})
	}
	return resp, nil
}

// readRevision loads one committed version; a side without a commit or
// without the file is absent.
func readRevision(ctx context.Context, rp *repo, rev, p string) ([]byte, bool, error) {
	if rev == "" {
		return nil, false, nil
	}
	var data []byte
	req := usecase.BlobRequest{Source: usecase.BlobRevision, Revision: rev, Path: p}
	err := rp.diff.ReadBlob(ctx, rp.root, req, func(_ diff.Entry, r io.Reader) error {
		var err error
		data, err = io.ReadAll(io.LimitReader(r, maxGodotMergeBytes+1))
		if err == nil && len(data) > maxGodotMergeBytes {
			err = clientDomain.NewUserError(fmt.Sprintf("%s is too large to merge", p))
		}
		return err
	})
	if errors.Is(err, usecase.ErrFileMissing) {
		return nil, false, nil
	}
	return data, err == nil, err
}
