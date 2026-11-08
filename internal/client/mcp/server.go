// Package mcp exposes the client CLI capabilities as a Model Context Protocol
// server over stdio. Read-only tools are always registered; mutating tools are
// registered per capability listed in Options.Allow.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

const (
	CapStage  = "stage"
	CapPush   = "push"
	CapSync   = "sync"
	CapBranch = "branch"
	CapLock   = "lock"
	CapMR     = "mr"
	CapMerge  = "merge"
)

var allCaps = []string{CapStage, CapPush, CapSync, CapBranch, CapLock, CapMR, CapMerge}

// AllCaps returns every write capability, which is what --allow-write grants.
func AllCaps() map[string]bool {
	out := make(map[string]bool, len(allCaps))
	for _, c := range allCaps {
		out[c] = true
	}
	return out
}

// ParseAllow parses a comma-separated capability list and rejects unknown
// names.
func ParseAllow(list string) (map[string]bool, error) {
	known := AllCaps()
	out := map[string]bool{}
	for _, raw := range strings.Split(list, ",") {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if !known[name] {
			sorted := append([]string(nil), allCaps...)
			sort.Strings(sorted)
			return nil, clientDomain.NewUserError(fmt.Sprintf("unknown capability %q (expected one of %s)", name, strings.Join(sorted, ", ")))
		}
		out[name] = true
	}
	return out, nil
}

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
	Tag       *usecase.Tag
	Update    *usecase.Update
	Merge     *usecase.Merge
	Connector Connector
	Auth      *usecase.Auth
	// Close releases the per-call client connection and local repository
	// handles. It may be nil.
	Close func()
}

func (u UseCases) close() {
	if u.Close != nil {
		u.Close()
	}
}

type Options struct {
	NewUseCases    func() UseCases
	RepoDir        string
	Allow          map[string]bool
	NoAgentTrailer bool
	Version        string
}

type server struct {
	opts    Options
	writeMu sync.Mutex
}

func NewServer(opts Options) *mcp.Server {
	s := &server{opts: opts}
	srv := mcp.NewServer(&mcp.Implementation{Name: "nipa", Version: opts.Version}, nil)
	addReadTools(srv, s)
	addMRReadTools(srv, s)
	addWriteTools(srv, s)
	addMRWriteTools(srv, s)
	addAssetTools(srv, s)
	return srv
}

func Serve(ctx context.Context, opts Options) error {
	return NewServer(opts).Run(ctx, &mcp.StdioTransport{})
}

func (s *server) allowed(capability string) bool {
	return s.opts.Allow[capability]
}

func (s *server) useCases() (UseCases, error) {
	if s.opts.NewUseCases == nil {
		return UseCases{}, errors.New("mcp server has no usecase factory configured")
	}
	return s.opts.NewUseCases(), nil
}

// open resolves the working copy root and builds the per-call usecase graph.
func (s *server) open(repo string) (string, UseCases, error) {
	root, err := s.repoRoot(repo)
	if err != nil {
		return "", UseCases{}, err
	}
	uc, err := s.useCases()
	if err != nil {
		return "", UseCases{}, err
	}
	return root, uc, nil
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
	wc, _, _, cleanup, err := s.localWorkingCopy(repo)
	return wc, cleanup, err
}

func (s *server) localWorkingCopy(repo string) (*usecase.WorkingCopy, *localrepo.LocalRepo, string, func(), error) {
	root, err := s.repoRoot(repo)
	if err != nil {
		return nil, nil, "", nil, err
	}
	lr := localrepo.NewLocalRepo()
	wc, err := usecase.NewWorkingCopy(lr, root)
	if err != nil {
		_ = lr.Close()
		return nil, nil, "", nil, err
	}
	return wc, lr, root, func() { _ = lr.Close() }, nil
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

const agentTrailerKey = "Nipa-Agent:"

// withAgentTrailer appends the MCP client identity to text written on the
// user's behalf. Blank text stays blank so usecase validation still applies.
func (s *server) withAgentTrailer(msg string, req *mcp.CallToolRequest) string {
	if s.opts.NoAgentTrailer || strings.TrimSpace(msg) == "" {
		return msg
	}
	agent := clientAgent(req)
	if agent == "" {
		return msg
	}
	for _, line := range strings.Split(msg, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), agentTrailerKey) {
			return msg
		}
	}
	return strings.TrimRight(msg, "\n") + "\n\n" + agentTrailerKey + " " + agent
}

func clientAgent(req *mcp.CallToolRequest) string {
	if req == nil || req.Session == nil {
		return ""
	}
	params := req.Session.InitializeParams()
	if params == nil || params.ClientInfo == nil {
		return ""
	}
	name := strings.TrimSpace(params.ClientInfo.Name)
	if name == "" {
		return ""
	}
	if version := strings.TrimSpace(params.ClientInfo.Version); version != "" {
		return name + "/" + version
	}
	return name
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

func notConfigured(what string) error {
	return toolError(clientDomain.NewUserError(what + " not configured"))
}

func readOnly() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true}
}

func nonDestructive() *mcp.ToolAnnotations {
	f := false
	return &mcp.ToolAnnotations{DestructiveHint: &f}
}

func destructive() *mcp.ToolAnnotations {
	t := true
	return &mcp.ToolAnnotations{DestructiveHint: &t}
}
