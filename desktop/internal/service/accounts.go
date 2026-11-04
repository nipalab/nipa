package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"google.golang.org/grpc"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type Account struct {
	Host     string `json:"host"`
	Address  string `json:"address"`
	SignedIn bool   `json:"signedIn"`
	Email    string `json:"email"`
	UserID   string `json:"userId"`
}

type RemoteProject struct {
	Org         string `json:"org"`
	OrgName     string `json:"orgName"`
	OrgRole     string `json:"orgRole"`
	Project     string `json:"project"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type RemoteBranch struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	Protected bool   `json:"protected"`
}

type RemoteDir struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type RemoteListing struct {
	Dirs      []RemoteDir `json:"dirs"`
	Files     int64       `json:"files"`
	FileBytes int64       `json:"fileBytes"`
}

// CloneRequest describes a checkout: Target is the new folder, Sparse the
// folders to download (empty for everything).
type CloneRequest struct {
	Server  string   `json:"server"`
	Org     string   `json:"org"`
	Project string   `json:"project"`
	Branch  string   `json:"branch"`
	Sparse  []string `json:"sparse"`
	Target  string   `json:"target"`
}

func (s *Service) daemon(ctx context.Context) (daemonpb.NipaDaemonClient, error) {
	sess, err := s.session(ctx)
	if err != nil {
		return nil, err
	}
	return sess.Client(), nil
}

func (s *Service) daemonCall(err error) error {
	if err == nil {
		return nil
	}
	s.dropIfDisconnected(err)
	return cleanError(err)
}

// AccountStatus reports whether the daemon holds a session for a server.
func (s *Service) AccountStatus(ctx context.Context, server string) (*Account, error) {
	client, err := s.daemon(ctx)
	if err != nil {
		return nil, err
	}
	res, err := client.AccountStatus(ctx, &daemonpb.AccountStatusRequest{Server: server})
	if err != nil {
		return nil, s.daemonCall(err)
	}
	return &Account{Host: res.GetHost(), Address: res.GetAddress(), SignedIn: res.GetSignedIn(), Email: res.GetEmail(), UserID: res.GetUserId()}, nil
}

// SignIn logs in with an email and password; the daemon stores the session.
func (s *Service) SignIn(ctx context.Context, server, email, password string) error {
	client, err := s.daemon(ctx)
	if err != nil {
		return err
	}
	_, err = client.Login(ctx, &daemonpb.LoginRequest{Host: server, Username: strings.TrimSpace(email), Password: password})
	return s.daemonCall(err)
}

// SignInSSH logs in with a registered SSH key from the agent or ~/.ssh.
func (s *Service) SignInSSH(ctx context.Context, server, email string) error {
	client, err := s.daemon(ctx)
	if err != nil {
		return err
	}
	_, err = client.LoginSSH(ctx, &daemonpb.LoginSSHRequest{Server: server, Email: email})
	return s.daemonCall(err)
}

// ListProjects lists the projects the signed-in account can read.
func (s *Service) ListProjects(ctx context.Context, server string) ([]RemoteProject, error) {
	client, err := s.daemon(ctx)
	if err != nil {
		return nil, err
	}
	res, err := client.ListProjects(ctx, &daemonpb.ListProjectsRequest{Server: server})
	if err != nil {
		return nil, s.daemonCall(err)
	}
	out := make([]RemoteProject, 0, len(res.GetProjects()))
	for _, p := range res.GetProjects() {
		out = append(out, RemoteProject{Org: p.GetOrg(), OrgName: p.GetOrgName(), OrgRole: p.GetOrgRole(), Project: p.GetProject(), Name: p.GetName(), Description: p.GetDescription()})
	}
	return out, nil
}

// RemoteBranches lists a project's branches.
func (s *Service) RemoteBranches(ctx context.Context, server, org, project string) ([]RemoteBranch, error) {
	client, err := s.daemon(ctx)
	if err != nil {
		return nil, err
	}
	res, err := client.RemoteBranches(ctx, &daemonpb.RemoteBranchesRequest{Server: server, Org: org, Project: project})
	if err != nil {
		return nil, s.daemonCall(err)
	}
	out := make([]RemoteBranch, 0, len(res.GetBranches()))
	for _, b := range res.GetBranches() {
		out = append(out, RemoteBranch{Name: b.GetName(), IsDefault: b.GetIsDefault(), Protected: b.GetIsProtected()})
	}
	return out, nil
}

// RemoteTree lists the folders of one directory on the server.
func (s *Service) RemoteTree(ctx context.Context, server, org, project, branch, path string) (*RemoteListing, error) {
	client, err := s.daemon(ctx)
	if err != nil {
		return nil, err
	}
	res, err := client.RemoteTree(ctx, &daemonpb.RemoteTreeRequest{Server: server, Org: org, Project: project, Branch: branch, Path: path})
	if err != nil {
		return nil, s.daemonCall(err)
	}
	out := &RemoteListing{Dirs: make([]RemoteDir, 0, len(res.GetDirs())), Files: res.GetFiles(), FileBytes: res.GetFileBytes()}
	for _, d := range res.GetDirs() {
		out.Dirs = append(out.Dirs, RemoteDir{Name: d.GetName(), Path: d.GetPath()})
	}
	return out, nil
}

// ChooseFolder asks for a folder (for example where a clone goes); an empty
// path means cancelled.
func (s *Service) ChooseFolder(_ context.Context, title string) (string, error) {
	if s.ChooseFolderFunc == nil {
		return "", errors.New("folder dialogs are not available")
	}
	return s.ChooseFolderFunc(title)
}

// Clone checks a project out into a new folder, reporting progress as
// op:progress events for the target folder, then opens it like AddRepo.
func (s *Service) Clone(ctx context.Context, req CloneRequest) (*Repo, error) {
	target := filepath.Clean(strings.TrimSpace(req.Target))
	if strings.TrimSpace(req.Target) == "" || !filepath.IsAbs(target) {
		return nil, errors.New("choose where the clone goes")
	}
	account, err := s.AccountStatus(ctx, req.Server)
	if err != nil {
		return nil, err
	}
	client, err := s.daemon(ctx)
	if err != nil {
		return nil, err
	}
	base := account.Address
	if !strings.Contains(base, "://") {
		base = "http://" + base // NOSONAR: bare host keeps cleartext h2c by design
	}
	url := base + "/" + req.Org + "/" + req.Project
	_, err = s.streamOp(ctx, client, target, "clone", func(c daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
		return c.Clone(ctx, &daemonpb.CloneRequest{Url: url, Branch: req.Branch, Sparse: req.Sparse, Target: target})
	})
	if err != nil {
		return nil, err
	}
	repo, err := s.AddRepo(ctx, target)
	if err != nil {
		return nil, err
	}
	return &repo, nil
}

// SetSparse changes which folders the clone downloads and updates it.
func (s *Service) SetSparse(ctx context.Context, root string, paths []string) (OpOutcome, error) {
	return s.runOp(ctx, root, "sparse", func(c daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
		return c.SetSparse(ctx, &daemonpb.SetSparseRequest{Root: root, Paths: paths})
	})
}
