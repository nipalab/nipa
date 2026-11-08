package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

// Session is a stored server session.
type Session struct {
	Email  string
	UserID string
}

// RemoteConn is a server connection that is not tied to a clone.
type RemoteConn interface {
	ServiceClient() (pb.NipaServiceClient, error)
	Close() error
}

// SSHLogin selects the key for an SSH login. KeyPath pins exactly one key
// file; the passphrase only lives for the duration of the request.
type SSHLogin struct {
	Email      string
	KeyPath    string
	Passphrase string
}

func (l SSHLogin) String() string {
	return fmt.Sprintf("SSHLogin{Email:%q KeyPath:%q}", l.Email, l.KeyPath)
}

func (l SSHLogin) GoString() string { return l.String() }

func (l SSHLogin) LogValue() slog.Value {
	return slog.GroupValue(slog.String("email", l.Email), slog.String("key_path", l.KeyPath))
}

// Accounts reaches servers before any clone exists: stored sessions, SSH key
// login, fresh connections for browsing, and cloning.
type Accounts struct {
	Session  func(host string) (Session, bool)
	LoginSSH func(ctx context.Context, address string, login SSHLogin) error
	Connect  func(ctx context.Context, address string) (RemoteConn, error)
	Clone    func(ctx context.Context, url, branch string, sparse []string, target string, progress usecase.DownloadProgress) error
}

// AccountStatus reports whether a session is stored for a server.
func (s *Server) AccountStatus(_ context.Context, req *daemonpb.AccountStatusRequest) (*daemonpb.AccountStatusResponse, error) {
	ep, err := clientDomain.ParseEndpoint(req.GetServer())
	if err != nil {
		return nil, toStatusError(err)
	}
	res := &daemonpb.AccountStatusResponse{Host: ep.Host, Address: ep.Address}
	if s.accounts.Session != nil {
		if sess, ok := s.accounts.Session(ep.Host); ok {
			res.SignedIn, res.Email, res.UserId = true, sess.Email, sess.UserID
		}
	}
	return res, nil
}

// LoginSSH signs in with a registered SSH key: the explicit key_path when
// set, otherwise the usual order (configured key, agent, ~/.ssh).
func (s *Server) LoginSSH(ctx context.Context, req *daemonpb.LoginSSHRequest) (*daemonpb.LoginResponse, error) {
	if s.accounts.LoginSSH == nil {
		return nil, status.Error(codes.FailedPrecondition, "SSH login is not configured")
	}
	ep, err := clientDomain.ParseEndpoint(req.GetServer())
	if err != nil {
		return nil, toStatusError(err)
	}
	login := SSHLogin{Email: strings.TrimSpace(req.GetEmail()), KeyPath: strings.TrimSpace(req.GetKeyPath()), Passphrase: req.GetPassphrase()}
	if login.Passphrase != "" && login.KeyPath == "" {
		return nil, status.Error(codes.InvalidArgument, "a passphrase needs key_path")
	}
	if err := s.accounts.LoginSSH(ctx, ep.Address, login); err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.LoginResponse{}, nil
}

func (s *Server) withRemote(ctx context.Context, server string, fn func(pb.NipaServiceClient) error) error {
	if s.accounts.Connect == nil {
		return status.Error(codes.FailedPrecondition, "server browsing is not configured")
	}
	ep, err := clientDomain.ParseEndpoint(server)
	if err != nil {
		return err
	}
	conn, err := s.accounts.Connect(ctx, ep.Address)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	client, err := conn.ServiceClient()
	if err != nil {
		return err
	}
	return fn(client)
}

// ListProjects lists the projects the signed-in account can read.
func (s *Server) ListProjects(ctx context.Context, req *daemonpb.ListProjectsRequest) (*daemonpb.ListProjectsResponse, error) {
	res := &daemonpb.ListProjectsResponse{}
	err := s.withRemote(ctx, req.GetServer(), func(client pb.NipaServiceClient) error {
		list, err := client.ListMyProjects(ctx, &pb.ListMyProjectsRequest{})
		res.Projects = list.GetProjects()
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return res, nil
}

// RemoteBranches lists a project's branches.
func (s *Server) RemoteBranches(ctx context.Context, req *daemonpb.RemoteBranchesRequest) (*daemonpb.RemoteBranchesResponse, error) {
	res := &daemonpb.RemoteBranchesResponse{}
	err := s.withRemote(ctx, req.GetServer(), func(client pb.NipaServiceClient) error {
		list, err := client.GetListBranch(ctx, &pb.GetListBranchRequest{Context: &pb.ProjectContext{Org: req.GetOrg(), Project: req.GetProject()}, Limit: 1000})
		res.Branches = list.GetBranches()
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return res, nil
}

// RemoteTree lists one directory of a branch: its subdirectories and how many
// files it holds directly, for choosing a sparse checkout.
func (s *Server) RemoteTree(ctx context.Context, req *daemonpb.RemoteTreeRequest) (*daemonpb.RemoteTreeResponse, error) {
	dir := strings.Trim(req.GetPath(), "/")
	res := &daemonpb.RemoteTreeResponse{}
	err := s.withRemote(ctx, req.GetServer(), func(client pb.NipaServiceClient) error {
		list, err := client.ListDirectory(ctx, &pb.ListDirectoryRequest{
			Context:  &pb.ProjectContext{Org: req.GetOrg(), Project: req.GetProject()},
			Revision: req.GetBranch(),
			Path:     dir,
		})
		if err != nil {
			return err
		}
		for _, e := range list.GetEntries() {
			if e.GetIsDir() {
				res.Dirs = append(res.Dirs, &daemonpb.RemoteTreeDir{Name: e.GetName(), Path: path.Join(dir, e.GetName())})
				continue
			}
			res.Files++
			res.FileBytes += e.GetSizeBytes()
		}
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	sort.Slice(res.Dirs, func(i, j int) bool {
		return strings.ToLower(res.Dirs[i].GetName()) < strings.ToLower(res.Dirs[j].GetName())
	})
	return res, nil
}

// Clone checks a repository out into a new folder, streaming download progress.
func (s *Server) Clone(req *daemonpb.CloneRequest, stream daemonpb.NipaDaemon_CloneServer) error {
	ctx := stream.Context()
	return streamOp(ctx, "clone", stream.Send, func(progress *progressAdapter) (*daemonpb.OpEvent, error) {
		if s.accounts.Clone == nil {
			return nil, status.Error(codes.FailedPrecondition, "clone is not configured")
		}
		target := strings.TrimSpace(req.GetTarget())
		if target == "" || !filepath.IsAbs(target) {
			return nil, clientDomain.NewUserError("choose an absolute folder to clone into")
		}
		target = filepath.Clean(target)
		if err := s.accounts.Clone(ctx, req.GetUrl(), req.GetBranch(), req.GetSparse(), target, progress); err != nil {
			return nil, err
		}
		lr := localrepo.NewLocalRepoWithTarget(target)
		defer func() { _ = lr.Close() }()
		if err := lr.Init(target); err != nil {
			return nil, err
		}
		cfg, err := lr.LoadConfig()
		if err != nil {
			return nil, err
		}
		commit := ""
		if pin, err := lr.LoadCommit(); err == nil && pin != nil {
			commit = pin.CommitID
		}
		return syncResultEvent(cfg.Branch, commit), nil
	})
}

// SetSparse changes the clone's sparse prefixes and updates the working copy
// to match: files outside the new set leave the disk, new ones arrive.
func (s *Server) SetSparse(req *daemonpb.SetSparseRequest, stream daemonpb.NipaDaemon_SetSparseServer) error {
	ctx := stream.Context()
	return s.runOp(ctx, req.GetRoot(), "sparse", stream.Send, func(rp *repo, progress *progressAdapter) (*daemonpb.OpEvent, error) {
		if rp.update == nil {
			return nil, status.Error(codes.FailedPrecondition, "update is not configured")
		}
		if err := rp.update.SetSparse(rp.root, req.GetPaths()); err != nil {
			return nil, err
		}
		if err := rp.update.Run(ctx, rp.root, progress); err != nil {
			return nil, err
		}
		return syncResultEvent(rp.config().Branch, rp.headCommitID()), nil
	})
}
