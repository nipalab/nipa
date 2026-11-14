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

	"github.com/nipalab/nipa/internal/assetdiff"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
)

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
	var refs [3]*assetdiff.Ref
	base, ours, theirs := usecase.ConflictSides(set, p)
	for i, side := range []*usecase.BlobRequest{base, ours, theirs} {
		if side != nil {
			refs[i] = &assetdiff.Ref{Source: string(side.Source), Revision: side.Revision, Path: p}
		}
	}
	m, err := assetdiff.MergeGodot(ctx, blobLoader(rp), refs[0], refs[1], refs[2])
	var mergeErr *assetdiff.MergeError
	switch {
	case errors.Is(err, assetdiff.ErrTooLarge):
		return nil, clientDomain.NewUserError(fmt.Sprintf("%s is too large to merge", p))
	case errors.As(err, &mergeErr):
		return nil, clientDomain.NewUserError(fmt.Sprintf("%s cannot be merged: %v", p, mergeErr.Err))
	case err != nil:
		return nil, err
	}
	resp := &daemonpb.MergeGodotResourceResponse{Merged: m.Merged, Clean: m.Clean}
	for _, c := range m.Conflicts {
		resp.Conflicts = append(resp.Conflicts, &daemonpb.GodotMergeConflict{Section: c.Section, Key: c.Key, Message: c.Message})
	}
	return resp, nil
}

// blobLoader reads file versions of the clone for assetdiff.
func blobLoader(rp *repo) assetdiff.Loader {
	return func(ctx context.Context, ref assetdiff.Ref, limit int64) ([]byte, error) {
		var data []byte
		req := usecase.BlobRequest{Source: usecase.BlobSource(ref.Source), Revision: ref.Revision, Path: ref.Path}
		err := rp.diff.ReadBlob(ctx, rp.root, req, func(_ diff.Entry, r io.Reader) error {
			var err error
			data, err = io.ReadAll(io.LimitReader(r, limit+1))
			if err == nil && int64(len(data)) > limit {
				err = assetdiff.ErrTooLarge
			}
			return err
		})
		if errors.Is(err, usecase.ErrFileMissing) {
			return nil, assetdiff.ErrMissing
		}
		return data, err
	}
}
