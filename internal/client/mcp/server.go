// Package mcp exposes the client CLI capabilities as a Model Context Protocol
// server over stdio. Read-only tools are always registered; mutating tools are
// only registered when the server starts with AllowWrite.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type Connector interface {
	Connect(ctx context.Context, host string) error
}

// UseCases is one per-call usecase graph. The factory in Options builds a fresh
// graph per tool call because the gRPC transport binds one host at a time.
type UseCases struct {
	Repo      *usecase.Repo
	Push      *usecase.Push
	Diff      *usecase.Diff
	MR        *usecase.MergeRequest
	Lock      *usecase.FileLock
	Connector Connector
}

type Options struct {
	NewUseCases func() UseCases
	RepoDir     string
	AllowWrite  bool
	Version     string
}

type server struct {
	opts    Options
	writeMu sync.Mutex
}

func NewServer(opts Options) *mcp.Server {
	s := &server{opts: opts}
	srv := mcp.NewServer(&mcp.Implementation{Name: "nipa", Version: opts.Version}, nil)
	addReadTools(srv, s)
	if opts.AllowWrite {
		addWriteTools(srv, s)
	}
	return srv
}

func Serve(ctx context.Context, opts Options) error {
	return NewServer(opts).Run(ctx, &mcp.StdioTransport{})
}

func (s *server) useCases() (UseCases, error) {
	if s.opts.NewUseCases == nil {
		return UseCases{}, errors.New("mcp server has no usecase factory configured")
	}
	return s.opts.NewUseCases(), nil
}

// repoRoot resolves the working copy root: the per-call override, then the
// --repo directory, then the process working directory.
func (s *server) repoRoot(override string) (string, error) {
	dir := override
	if dir == "" {
		dir = s.opts.RepoDir
	}
	if dir == "" {
		return localrepo.FindRepoRoot()
	}
	return localrepo.FindRepoRootFrom(dir)
}

func (s *server) workingCopy(repo string) (*usecase.WorkingCopy, func(), error) {
	root, err := s.repoRoot(repo)
	if err != nil {
		return nil, nil, err
	}
	lr := localrepo.NewLocalRepo()
	wc, err := usecase.NewWorkingCopy(lr, root)
	if err != nil {
		_ = lr.Close()
		return nil, nil, err
	}
	return wc, func() { _ = lr.Close() }, nil
}

func loadConfig(root string) (*clientDomain.Config, error) {
	return localrepo.NewLocalRepoWithTarget(root).LoadConfig()
}

func loadPinnedCommit(root string) (*clientDomain.LocalCommit, error) {
	lr := localrepo.NewLocalRepo()
	defer func() { _ = lr.Close() }()
	if err := lr.Init(root); err != nil {
		return nil, err
	}
	return lr.LoadCommit()
}

// toolError keeps domain hints and next-step actions visible to the agent,
// since the MCP SDK only forwards the error text of a tool failure.
func toolError(err error) error {
	if err == nil {
		return nil
	}
	var domErr *clientDomain.Error
	if !errors.As(err, &domErr) {
		return err
	}
	var parts []string
	if domErr.Hint != "" {
		parts = append(parts, "hint: "+domErr.Hint)
	}
	if domErr.Action != "" {
		parts = append(parts, "next: "+domErr.Action)
	}
	if len(parts) == 0 {
		return err
	}
	return fmt.Errorf("%w (%s)", err, strings.Join(parts, "; "))
}

func readOnly() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true}
}
