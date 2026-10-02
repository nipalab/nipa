package daemon

import (
	"context"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
)

// WatchRepo registers a clone (idempotently) and returns its cached config.
func (s *Server) WatchRepo(_ context.Context, req *daemonpb.WatchRepoRequest) (*daemonpb.WatchRepoResponse, error) {
	rp, err := s.repos.watch(req.GetRoot())
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.WatchRepoResponse{Repo: rp.info()}, nil
}

// UnwatchRepo drops a clone; in-flight operations are allowed to finish first.
func (s *Server) UnwatchRepo(_ context.Context, req *daemonpb.UnwatchRepoRequest) (*daemonpb.UnwatchRepoResponse, error) {
	if err := s.repos.unwatch(req.GetRoot()); err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.UnwatchRepoResponse{}, nil
}

// ListRepos lists every watched clone, sorted by root.
func (s *Server) ListRepos(context.Context, *daemonpb.ListReposRequest) (*daemonpb.ListReposResponse, error) {
	repos := s.repos.list()
	res := &daemonpb.ListReposResponse{Repos: make([]*daemonpb.RepoInfo, 0, len(repos))}
	for _, rp := range repos {
		res.Repos = append(res.Repos, rp.info())
	}
	return res, nil
}

// Status returns the working-copy status from the clone's local snapshot and
// stat cache; no_cache forces a full rehash of every tracked file.
func (s *Server) Status(ctx context.Context, req *daemonpb.StatusRequest) (*daemonpb.StatusResponse, error) {
	var status *clientDomain.Status
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		st, err := rp.wc.Status(ctx, usecase.StatusOptions{NoCache: req.GetNoCache()})
		if err != nil {
			return err
		}
		status = st
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return statusToPB(status), nil
}

// Stage adds paths to the staged set, unstages others, and returns the
// refreshed status.
func (s *Server) Stage(ctx context.Context, req *daemonpb.StageRequest) (*daemonpb.StatusResponse, error) {
	var status *clientDomain.Status
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		if adds := req.GetAdd(); len(adds) > 0 {
			if err := rp.wc.Add(ctx, adds); err != nil {
				return err
			}
		}
		if removes := req.GetUnstage(); len(removes) > 0 {
			if err := rp.wc.Remove(ctx, removes); err != nil {
				return err
			}
		}
		st, err := rp.wc.Status(ctx)
		if err != nil {
			return err
		}
		status = st
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return statusToPB(status), nil
}

func statusToPB(st *clientDomain.Status) *daemonpb.StatusResponse {
	resp := &daemonpb.StatusResponse{
		Branch:    st.Branch,
		Staged:    st.Staged,
		Deleted:   st.Deleted,
		Modified:  st.Modified,
		Untracked: st.Untracked,
		Missing:   st.Missing,
		Conflicts: st.Conflicts,
	}
	if st.Head != nil {
		resp.Head = &daemonpb.StatusHead{Kind: st.Head.Kind, Name: st.Head.Name}
	}
	return resp
}
