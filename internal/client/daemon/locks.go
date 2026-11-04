package daemon

import (
	"context"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

// Identity names the signed-in account of a proxy connection.
type Identity interface {
	UserID(ctx context.Context) (string, error)
}

// LockStatus lists the project's locks and, for every changed binary in the
// clone, whether the configured branch's lock scope lets it be pushed: covered
// by the caller's lock, held by someone else, or a tracked binary still
// needing one.
func (s *Server) LockStatus(ctx context.Context, req *daemonpb.LockStatusRequest) (*daemonpb.LockStatusResponse, error) {
	var st *clientDomain.Status
	var snap *clientDomain.Snapshot
	var ident Identity
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		var err error
		if st, err = rp.wc.Status(ctx); err != nil {
			return err
		}
		if snap, err = rp.localRepo.Snapshot(); err != nil {
			return err
		}
		ident, _ = rp.proxy.(Identity)
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	if ident == nil {
		return nil, status.Error(codes.FailedPrecondition, "proxy is not configured")
	}
	res := &daemonpb.LockStatusResponse{Branch: st.Branch}
	err = s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		locks, err := client.ListFileLocks(ctx, &pb.ListFileLocksRequest{Context: project})
		if err != nil {
			return err
		}
		res.Locks = locks.GetLocks()
		if st.Branch != "" {
			branch, err := client.GetBranchByName(ctx, &pb.GetBranchByNameRequest{Context: project, Name: st.Branch})
			if err != nil && status.Code(err) != codes.NotFound {
				return err
			}
			res.DefaultBranch = branch.GetBranch().GetIsDefault()
		}
		perms, err := client.GetMyPermissions(ctx, &pb.GetMyPermissionsRequest{Context: project})
		if err != nil {
			return err
		}
		res.CanReleaseAny = perms.GetProjectPermission()&uint64(domain.PermissionAdmin) != 0
		res.UserId, err = ident.UserID(ctx)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	res.Files = fileLockStates(st, snap, scopeLocks(res), res.GetUserId())
	return res, nil
}

func scopeLocks(res *daemonpb.LockStatusResponse) []*pb.FileLockDetail {
	var out []*pb.FileLockDetail
	for _, l := range res.GetLocks() {
		if res.GetDefaultBranch() && l.GetGlobal() || !res.GetDefaultBranch() && !l.GetGlobal() && l.GetBranch() == res.GetBranch() {
			out = append(out, l)
		}
	}
	return out
}

func fileLockStates(st *clientDomain.Status, snap *clientDomain.Snapshot, locks []*pb.FileLockDetail, userID string) []*daemonpb.FileLockState {
	binary := map[string]bool{}
	for _, f := range snap.Files {
		binary[f.Path] = f.IsBinary
	}
	changed := map[string]bool{}
	for _, list := range [][]string{st.Staged, st.Deleted, st.Modified, st.Missing, st.Untracked} {
		for _, p := range list {
			changed[p] = true
		}
	}
	paths := make([]string, 0, len(changed))
	for p := range changed {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := []*daemonpb.FileLockState{}
	for _, p := range paths {
		isBinary, tracked := binary[p]
		if !isBinary && !chunker.IsLockablePath(p) {
			continue
		}
		f := &daemonpb.FileLockState{Path: p, Tracked: tracked, State: "free"}
		if lock := coveringLock(locks, p); lock != nil {
			f.LockPath, f.HeldBy, f.HeldByName = lock.GetPath(), lock.GetHeldBy(), lock.GetHeldByName()
			f.State = "theirs"
			if lock.GetHeldBy() == userID {
				f.State = "mine"
			}
		} else if tracked {
			f.State = "needed"
		}
		out = append(out, f)
	}
	return out
}

func coveringLock(locks []*pb.FileLockDetail, path string) *pb.FileLockDetail {
	for _, l := range locks {
		if domain.PrefixCovers(l.GetPath(), path) {
			return l
		}
	}
	return nil
}
