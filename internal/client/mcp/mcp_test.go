package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func connect(t *testing.T, opts Options) *mcp.ClientSession {
	t.Helper()
	srv := NewServer(opts)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	return res
}

func toolText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected text content, got %T", res.Content[0])
	return text.Text
}

func toolNames(t *testing.T, session *mcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

func offlineUseCases() UseCases {
	return UseCases{Diff: usecase.NewDiff(nil, nil, localrepo.NewLocalRepo())}
}

type fakeConnector struct{ err error }

func (f fakeConnector) Connect(context.Context, string) error { return f.err }

type fakeLogin struct{}

func (fakeLogin) LoginWithUsernamePassword(context.Context, string, string, string) (*domain.LoginResult, error) {
	return nil, errors.New("login not expected")
}

type fakeStorage struct{}

func (fakeStorage) SaveToken(*domain.LoginResult) error { return nil }

func (fakeStorage) LoadToken(string) (*domain.LoginResult, error) {
	return &domain.LoginResult{AccessToken: testAccessToken()}, nil
}

func testAccessToken() string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":9999999999}`))
	return "e30." + payload + ".signature"
}

func loggedInAuth() *usecase.Auth {
	return usecase.NewAuth(fakeLogin{}, fakeStorage{}, nil)
}

type fakeLockClient struct {
	locks   []*domain.FileLock
	listErr error
}

func (fakeLockClient) Connect(context.Context, string) error { return nil }

func (fakeLockClient) LockFile(_ context.Context, _, _, path, branch string) (*domain.FileLock, error) {
	return &domain.FileLock{Path: path, Branch: branch, HeldByName: "alice"}, nil
}

func (fakeLockClient) UnlockFile(context.Context, string, string, string, string) error { return nil }

func (f fakeLockClient) ListFileLocks(context.Context, string, string) ([]*domain.FileLock, error) {
	return f.locks, f.listErr
}

type fakeRepoClient struct {
	branches []*serverDomain.Branch
	log      []*serverDomain.CommitLogEntry
	err      error
}

func (fakeRepoClient) GetDefaultBranch(context.Context, string, string) (*serverDomain.Branch, error) {
	return &serverDomain.Branch{Name: "main"}, nil
}

func (fakeRepoClient) GetBranchByName(_ context.Context, _, _, name string) (*serverDomain.Branch, error) {
	return &serverDomain.Branch{Name: name}, nil
}

func (fakeRepoClient) GetTreeNodeManifest(context.Context, string, string, string, []string) (*serverDomain.TreeNode, error) {
	return &serverDomain.TreeNode{}, nil
}

func (f fakeRepoClient) ListBranches(context.Context, string, string) ([]*serverDomain.Branch, error) {
	return f.branches, f.err
}

func (fakeRepoClient) CreateBranch(_ context.Context, _, _, name, _, _, _ string) (*serverDomain.Branch, error) {
	id := snow.ID(9001)
	return &serverDomain.Branch{Name: name, CommitID: &id}, nil
}

func (fakeRepoClient) DeleteBranch(context.Context, string, string, string) error { return nil }

func (fakeRepoClient) DownloadChunks(context.Context, domain.ChunkScope, []serverDomain.Hash, func(serverDomain.Hash, []byte) error) error {
	return nil
}

func (f fakeRepoClient) GetCommitLog(context.Context, string, string, string, *snow.ID, int) ([]*serverDomain.CommitLogEntry, error) {
	return f.log, f.err
}

type fakeMRClient struct {
	requests []*domain.MergeRequest
	err      error
}

func (fakeMRClient) Connect(context.Context, string) error { return nil }

func (fakeMRClient) CreateMergeRequest(context.Context, string, string, string, string, string, string, bool) (*domain.MergeRequest, error) {
	return &domain.MergeRequest{Number: 1, Title: "Add b", Status: domain.MergeRequestOpen}, nil
}

func (fakeMRClient) UpdateMergeRequest(context.Context, string, string, int64, string, string, *bool) (*domain.MergeRequest, error) {
	return nil, errors.New("not implemented")
}

func (f fakeMRClient) ListMergeRequests(context.Context, string, string, domain.ListMergeRequestOptions) ([]*domain.MergeRequest, int64, error) {
	return f.requests, 0, f.err
}

func (fakeMRClient) MergeMergeRequest(context.Context, string, string, int64, string, bool) (*domain.MergeRequest, *domain.Mergeability, error) {
	return nil, nil, errors.New("not implemented")
}

func (fakeMRClient) CloseMergeRequest(context.Context, string, string, int64) (*domain.MergeRequest, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) GetDefaultBranch(context.Context, string, string) (*serverDomain.Branch, error) {
	return &serverDomain.Branch{Name: "main"}, nil
}

func (fakeMRClient) GetMergeRequest(context.Context, string, string, int64) (*domain.MergeRequest, *domain.Mergeability, error) {
	return nil, nil, errors.New("not implemented")
}

func (fakeMRClient) ReopenMergeRequest(context.Context, string, string, int64) (*domain.MergeRequest, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) CheckMergeRequest(context.Context, string, string, int64) (*domain.Mergeability, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) ListMergeRequestCommits(context.Context, string, string, int64) ([]*serverDomain.CommitLogEntry, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) GetMergeRequestDiff(context.Context, string, string, int64) ([]*domain.MergeRequestDiffFile, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) ListMergeRequestReviews(context.Context, string, string, int64) ([]*domain.MergeRequestReview, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) SubmitMergeRequestReview(context.Context, string, string, int64, string, string) (*domain.MergeRequestReview, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) ListMergeRequestThreads(context.Context, string, string, int64) ([]*domain.MergeRequestThread, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) AddMergeRequestComment(context.Context, string, string, int64, string, *int64, *int64, string) (*domain.MergeRequestThread, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) ReplyMergeRequestThread(context.Context, string, string, int64, string, string) (*domain.MergeRequestComment, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) ResolveMergeRequestThread(context.Context, string, string, int64, string, bool) (*domain.MergeRequestThread, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) ListMergeRequestTimeline(context.Context, string, string, int64) ([]*domain.MergeRequestTimelineItem, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) ListMergeRequestReviewRequests(context.Context, string, string, int64) ([]*domain.MergeRequestReviewRequest, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) RequestMergeRequestReview(context.Context, string, string, int64, string) (*domain.MergeRequestReviewRequest, error) {
	return nil, errors.New("not implemented")
}

func (fakeMRClient) RemoveMergeRequestReviewRequest(context.Context, string, string, int64, string) error {
	return errors.New("not implemented")
}

func setupRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	var children []*serverDomain.File
	var pending []*serverDomain.ChunkData
	for _, name := range names {
		content := []byte(files[name])
		chunks, err := chunker.ChunkAll(content)
		require.NoError(t, err)
		hashes := make([]serverDomain.Hash, len(chunks))
		fileChunks := make([]serverDomain.Chunk, len(chunks))
		for i, c := range chunks {
			hashes[i] = c.Hash
			fileChunks[i] = serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))}
			pending = append(pending, &serverDomain.ChunkData{Hash: c.Hash, Data: c.Data})
		}
		children = append(children, &serverDomain.File{
			Name:      name,
			Mode:      2,
			SizeBytes: int64(len(content)),
			IsBinary:  chunker.IsBinary(content),
			Hash:      chunker.FileHash(hashes),
			Chunks:    fileChunks,
		})
	}
	require.NoError(t, lr.StoreChunks(pending))
	require.NoError(t, lr.SaveTree(&serverDomain.TreeNode{
		Name:         "",
		Hash:         chunker.Sum([]byte("snapshot")),
		FileChildren: children,
	}))
	require.NoError(t, lr.Close())

	for name, content := range files {
		writeFile(t, root, name, content)
	}
	return root
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestNewServer_ReadOnlyTools(t *testing.T) {
	session := connect(t, Options{NewUseCases: offlineUseCases})
	names := toolNames(t, session)
	for _, want := range []string{"nipa_status", "nipa_diff", "nipa_log", "nipa_branch_list", "nipa_mr_list", "nipa_lock_list"} {
		require.True(t, names[want], "expected read tool %s", want)
	}
	for _, want := range []string{"nipa_add", "nipa_push", "nipa_branch_create", "nipa_lock", "nipa_unlock", "nipa_mr_create"} {
		require.False(t, names[want], "write tool %s must be gated", want)
	}
}

func TestNewServer_WriteToolsGated(t *testing.T) {
	session := connect(t, Options{NewUseCases: offlineUseCases, AllowWrite: true})
	names := toolNames(t, session)
	for _, want := range []string{"nipa_add", "nipa_push", "nipa_branch_create", "nipa_lock", "nipa_unlock", "nipa_mr_create"} {
		require.True(t, names[want], "expected write tool %s", want)
	}
}

func TestStatus(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "one\n"})
	writeFile(t, root, "a.txt", "two\n")
	writeFile(t, root, "b.txt", "new\n")
	session := connect(t, Options{NewUseCases: offlineUseCases})

	res := call(t, session, "nipa_status", map[string]any{"repo": root})
	require.False(t, res.IsError)

	var st output.Status
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &st))
	require.Equal(t, []string{"a.txt"}, st.Modified)
	require.Equal(t, []string{"b.txt"}, st.Untracked)
}

func TestStatus_OutsideRepo(t *testing.T) {
	session := connect(t, Options{NewUseCases: offlineUseCases})
	res := call(t, session, "nipa_status", map[string]any{"repo": t.TempDir()})
	require.True(t, res.IsError)
	text := toolText(t, res)
	require.Contains(t, text, "not a nipa repository")
	require.Contains(t, text, "nipa clone")
}

func TestDiff(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "one\ntwo\nthree\n"})
	writeFile(t, root, "a.txt", "one\nTWO\nthree\n")
	session := connect(t, Options{NewUseCases: offlineUseCases})

	res := call(t, session, "nipa_diff", map[string]any{"repo": root})
	require.False(t, res.IsError)

	var d output.Diff
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &d))
	require.Len(t, d.Changes, 1)
	require.Equal(t, "M", d.Changes[0].Status)
	require.Len(t, d.Changes[0].Hunks, 1)
	require.Equal(t, "add", d.Changes[0].Hunks[0].Lines[2].Kind)
}

func TestDiff_Validation(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "a\n"})
	session := connect(t, Options{NewUseCases: offlineUseCases})

	res := call(t, session, "nipa_diff", map[string]any{"repo": root, "revisions": []string{"a", "b", "c"}})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "too many revisions")

	res = call(t, session, "nipa_diff", map[string]any{"repo": root, "revisions": []string{"main"}, "staged": true})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "staged cannot be combined with revisions")
}

func TestLog_ConnectError(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{NewUseCases: func() UseCases {
		return UseCases{
			Repo:      usecase.NewRepo(nil, nil, localrepo.NewLocalRepo()),
			Connector: fakeConnector{err: errors.New("dial failed")},
		}
	}})

	res := call(t, session, "nipa_log", map[string]any{"repo": root})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "dial failed")
}

func TestBranchList_ConnectError(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{NewUseCases: func() UseCases {
		return UseCases{
			Repo:      usecase.NewRepo(nil, nil, localrepo.NewLocalRepo()),
			Connector: fakeConnector{err: errors.New("dial failed")},
		}
	}})

	res := call(t, session, "nipa_branch_list", map[string]any{"repo": root})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "dial failed")
}

func TestMrList_InvalidStatus(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{NewUseCases: func() UseCases {
		return UseCases{MR: usecase.NewMergeRequest(nil, nil, localrepo.NewLocalRepo())}
	}})

	res := call(t, session, "nipa_mr_list", map[string]any{"repo": root, "status": "bogus"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "status must be one of")
}

func TestMrCreate_EmptyTitle(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{AllowWrite: true, NewUseCases: func() UseCases {
		return UseCases{MR: usecase.NewMergeRequest(nil, nil, localrepo.NewLocalRepo())}
	}})

	res := call(t, session, "nipa_mr_create", map[string]any{"repo": root, "title": "  "})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "a title is required")
}

func TestLockList(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{NewUseCases: func() UseCases {
		client := fakeLockClient{locks: []*domain.FileLock{
			{Path: "art/tex.png", Global: true, HeldByName: "bob"},
			{Path: "audio/loop.wav", Branch: "main"},
		}}
		return UseCases{Lock: usecase.NewFileLock(loggedInAuth(), client, localrepo.NewLocalRepo())}
	}})

	res := call(t, session, "nipa_lock_list", map[string]any{"repo": root})
	require.False(t, res.IsError)

	var locks output.Locks
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &locks))
	require.Len(t, locks.Locks, 2)
	require.Equal(t, "mainline", locks.Locks[0].Scope)
	require.Equal(t, "branch", locks.Locks[1].Scope)
}

func TestLock_EmptyPath(t *testing.T) {
	root := setupRepo(t, nil)
	client := fakeLockClient{}
	session := connect(t, Options{AllowWrite: true, NewUseCases: func() UseCases {
		return UseCases{Lock: usecase.NewFileLock(nil, client, localrepo.NewLocalRepo())}
	}})

	res := call(t, session, "nipa_lock", map[string]any{"repo": root, "path": "  "})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "a path is required")
}

func TestLock(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{AllowWrite: true, NewUseCases: func() UseCases {
		return UseCases{Lock: usecase.NewFileLock(loggedInAuth(), fakeLockClient{}, localrepo.NewLocalRepo())}
	}})

	res := call(t, session, "nipa_lock", map[string]any{"repo": root, "path": "art/tex.png"})
	require.False(t, res.IsError)

	var lock output.Lock
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &lock))
	require.Equal(t, "art/tex.png", lock.Path)
	require.Equal(t, "branch", lock.Scope)
	require.Equal(t, "alice", lock.HeldByName)
}

func TestUnlock(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{AllowWrite: true, NewUseCases: func() UseCases {
		return UseCases{Lock: usecase.NewFileLock(loggedInAuth(), fakeLockClient{}, localrepo.NewLocalRepo())}
	}})

	res := call(t, session, "nipa_unlock", map[string]any{"repo": root, "path": "art/tex.png"})
	require.False(t, res.IsError)

	var out unlockedOutput
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &out))
	require.Equal(t, "art/tex.png", out.Path)
}

func TestAdd_StagesAndReportsStatus(t *testing.T) {
	root := setupRepo(t, nil)
	writeFile(t, root, "new.txt", "hello\n")
	session := connect(t, Options{AllowWrite: true, NewUseCases: offlineUseCases})

	res := call(t, session, "nipa_add", map[string]any{"repo": root, "paths": []string{"new.txt"}})
	require.False(t, res.IsError)

	var st output.Status
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &st))
	require.Equal(t, []string{"new.txt"}, st.Staged)
}

func TestAdd_EmptyPaths(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{AllowWrite: true, NewUseCases: offlineUseCases})

	res := call(t, session, "nipa_add", map[string]any{"repo": root, "paths": []string{}})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "at least one path is required")
}

func TestPush_EmptyMessage(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{AllowWrite: true, NewUseCases: func() UseCases {
		return UseCases{Push: usecase.NewPush(nil, nil, localrepo.NewLocalRepo())}
	}})

	res := call(t, session, "nipa_push", map[string]any{"repo": root, "message": "  "})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "commit message is required")
}

func TestBranchCreate_EmptyName(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{AllowWrite: true, NewUseCases: func() UseCases {
		return UseCases{
			Repo:      usecase.NewRepo(nil, nil, localrepo.NewLocalRepo()),
			Connector: fakeConnector{},
		}
	}})

	res := call(t, session, "nipa_branch_create", map[string]any{"repo": root, "name": " "})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "branch name is required")
}

func TestWriteTool_NotRegisteredWithoutAllowWrite(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{NewUseCases: offlineUseCases})

	_, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "nipa_push", Arguments: map[string]any{"repo": root, "message": "m"}})
	require.Error(t, err)
}

func TestLog_Success(t *testing.T) {
	root := setupRepo(t, nil)
	entries := []*serverDomain.CommitLogEntry{
		{
			Commit: serverDomain.Commit{
				ID:        9001,
				Message:   "first commit",
				CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			AuthorName: "Alice",
		},
	}
	session := connect(t, Options{NewUseCases: func() UseCases {
		return UseCases{
			Repo:      usecase.NewRepo(loggedInAuth(), fakeRepoClient{log: entries}, localrepo.NewLocalRepo()),
			Connector: fakeConnector{},
		}
	}})

	res := call(t, session, "nipa_log", map[string]any{"repo": root, "limit": 5})
	require.False(t, res.IsError)

	var log output.Log
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &log))
	require.Len(t, log.Commits, 1)
	require.Equal(t, "6y1", log.Commits[0].ID)
	require.Equal(t, "Alice", log.Commits[0].AuthorName)
}

func TestLog_NotConfigured(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{NewUseCases: func() UseCases { return UseCases{} }})

	res := call(t, session, "nipa_log", map[string]any{"repo": root})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "server connection is not configured")
}

func TestBranchList_Success(t *testing.T) {
	root := setupRepo(t, nil)
	branches := []*serverDomain.Branch{
		{Name: "main", IsDefault: true},
		{Name: "dev", IsProtected: true},
	}
	session := connect(t, Options{NewUseCases: func() UseCases {
		return UseCases{
			Repo:      usecase.NewRepo(loggedInAuth(), fakeRepoClient{branches: branches}, localrepo.NewLocalRepo()),
			Connector: fakeConnector{},
		}
	}})

	res := call(t, session, "nipa_branch_list", map[string]any{"repo": root})
	require.False(t, res.IsError)

	var out output.Branches
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &out))
	require.Equal(t, "main", out.Current)
	require.Len(t, out.Branches, 2)
	require.True(t, out.Branches[0].Current)
	require.True(t, out.Branches[1].Protected)
}

func TestBranchCreate_Success(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{AllowWrite: true, NewUseCases: func() UseCases {
		return UseCases{
			Repo:      usecase.NewRepo(loggedInAuth(), fakeRepoClient{}, localrepo.NewLocalRepo()),
			Connector: fakeConnector{},
		}
	}})

	res := call(t, session, "nipa_branch_create", map[string]any{"repo": root, "name": "feature"})
	require.False(t, res.IsError)

	var out branchCreatedOutput
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &out))
	require.Equal(t, "feature", out.Name)
	require.Equal(t, "6y1", out.CommitID)
}

func TestMrList_Success(t *testing.T) {
	root := setupRepo(t, nil)
	requests := []*domain.MergeRequest{
		{ID: "abc", Number: 1, SourceBranch: "feature", TargetBranch: "main", Title: "Add b", Status: domain.MergeRequestOpen},
	}
	session := connect(t, Options{NewUseCases: func() UseCases {
		return UseCases{MR: usecase.NewMergeRequest(loggedInAuth(), fakeMRClient{requests: requests}, localrepo.NewLocalRepo())}
	}})

	res := call(t, session, "nipa_mr_list", map[string]any{"repo": root})
	require.False(t, res.IsError)

	var out output.MergeRequests
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &out))
	require.Len(t, out.MergeRequests, 1)
	require.Equal(t, "Add b", out.MergeRequests[0].Title)
}

func TestMrCreate_Success(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{AllowWrite: true, NewUseCases: func() UseCases {
		return UseCases{MR: usecase.NewMergeRequest(loggedInAuth(), fakeMRClient{}, localrepo.NewLocalRepo())}
	}})

	res := call(t, session, "nipa_mr_create", map[string]any{"repo": root, "title": "Add b", "target": "release"})
	require.False(t, res.IsError)

	var out output.MergeRequest
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &out))
	require.Equal(t, int64(1), out.Number)
	require.Equal(t, "open", out.Status)
}

func TestStatus_FromRepoDir(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "one\n"})
	writeFile(t, root, "b.txt", "new\n")
	session := connect(t, Options{RepoDir: root, NewUseCases: offlineUseCases})

	res := call(t, session, "nipa_status", nil)
	require.False(t, res.IsError)

	var st output.Status
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &st))
	require.Equal(t, []string{"b.txt"}, st.Untracked)
}

func TestStatus_FromWorkingDirectory(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "one\n"})
	writeFile(t, root, "b.txt", "new\n")
	t.Chdir(root)
	session := connect(t, Options{NewUseCases: offlineUseCases})

	res := call(t, session, "nipa_status", nil)
	require.False(t, res.IsError)

	var st output.Status
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &st))
	require.Equal(t, []string{"b.txt"}, st.Untracked)
}

type fakePushClient struct{}

func (fakePushClient) Connect(context.Context, string) error { return nil }

func (fakePushClient) Push(context.Context, string, string, string, string, string, []*serverDomain.PushFile, []string, string, string) (*serverDomain.PushResult, error) {
	return &serverDomain.PushResult{CommitID: snow.ID(9002), CommitHash: serverDomain.Hash{0x01}}, nil
}

func (fakePushClient) UploadChunks(context.Context, domain.ChunkScope, []*serverDomain.ChunkData, ...func(*serverDomain.ChunkData)) (int, int, error) {
	return 0, 0, nil
}

func (fakePushClient) GetTreeNodeManifest(context.Context, string, string, string, []string) (*serverDomain.TreeNode, error) {
	return &serverDomain.TreeNode{}, nil
}

func stageFile(t *testing.T, root, path string) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	defer lr.Close()
	require.NoError(t, lr.StageAdd(path))
}

func TestUseCases_ClosedAfterCall(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "one\n"})
	writeFile(t, root, "a.txt", "two\n")
	closed := false
	session := connect(t, Options{NewUseCases: func() UseCases {
		uc := UseCases{Diff: usecase.NewDiff(nil, nil, localrepo.NewLocalRepo())}
		uc.Close = func() { closed = true }
		return uc
	}})

	res := call(t, session, "nipa_diff", map[string]any{"repo": root})
	require.False(t, res.IsError)
	require.True(t, closed)
}

func TestConnectContext_ClosesOnConnectError(t *testing.T) {
	root := setupRepo(t, nil)
	closed := false
	session := connect(t, Options{NewUseCases: func() UseCases {
		uc := UseCases{
			Repo:      usecase.NewRepo(nil, nil, localrepo.NewLocalRepo()),
			Connector: fakeConnector{err: errors.New("dial failed")},
		}
		uc.Close = func() { closed = true }
		return uc
	}})

	res := call(t, session, "nipa_log", map[string]any{"repo": root})
	require.True(t, res.IsError)
	require.True(t, closed)
}

func TestTools_NotConfigured(t *testing.T) {
	root := setupRepo(t, nil)
	empty := func() UseCases { return UseCases{} }
	readSession := connect(t, Options{NewUseCases: empty})
	writeSession := connect(t, Options{AllowWrite: true, NewUseCases: empty})

	cases := []struct {
		name    string
		session *mcp.ClientSession
		tool    string
		args    map[string]any
		want    string
	}{
		{"diff", readSession, "nipa_diff", map[string]any{"repo": root}, "diff is not configured"},
		{"mr list", readSession, "nipa_mr_list", map[string]any{"repo": root}, "merge requests are not configured"},
		{"lock list", readSession, "nipa_lock_list", map[string]any{"repo": root}, "locks are not configured"},
		{"push", writeSession, "nipa_push", map[string]any{"repo": root, "message": "m"}, "push is not configured"},
		{"lock", writeSession, "nipa_lock", map[string]any{"repo": root, "path": "a.psd"}, "locks are not configured"},
		{"unlock", writeSession, "nipa_unlock", map[string]any{"repo": root, "path": "a.psd"}, "locks are not configured"},
		{"mr create", writeSession, "nipa_mr_create", map[string]any{"repo": root, "title": "t"}, "merge requests are not configured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := call(t, tc.session, tc.tool, tc.args)
			require.True(t, res.IsError)
			require.Contains(t, toolText(t, res), tc.want)
		})
	}
}

func TestPush_Success(t *testing.T) {
	root := setupRepo(t, nil)
	writeFile(t, root, "a.txt", "hello\n")
	stageFile(t, root, "a.txt")
	session := connect(t, Options{AllowWrite: true, NewUseCases: func() UseCases {
		return UseCases{Push: usecase.NewPush(loggedInAuth(), fakePushClient{}, localrepo.NewLocalRepo())}
	}})

	res := call(t, session, "nipa_push", map[string]any{"repo": root, "message": "add a"})
	require.False(t, res.IsError)

	var out pushOutput
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &out))
	require.Equal(t, "main", out.Branch)
	require.Equal(t, "6y2", out.CommitID)
}

func TestTools_NoFactory(t *testing.T) {
	root := setupRepo(t, nil)
	readSession := connect(t, Options{})
	writeSession := connect(t, Options{AllowWrite: true})

	cases := []struct {
		name    string
		session *mcp.ClientSession
		tool    string
		args    map[string]any
	}{
		{"diff", readSession, "nipa_diff", map[string]any{"repo": root}},
		{"push", writeSession, "nipa_push", map[string]any{"repo": root, "message": "m"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := call(t, tc.session, tc.tool, tc.args)
			require.True(t, res.IsError)
			require.Contains(t, toolText(t, res), "no usecase factory configured")
		})
	}
}

func TestOnlineTools_PropagateErrors(t *testing.T) {
	root := setupRepo(t, nil)
	want := errors.New("server exploded")
	cases := []struct {
		name string
		uc   func() UseCases
		tool string
	}{
		{
			name: "log",
			uc: func() UseCases {
				return UseCases{
					Repo:      usecase.NewRepo(loggedInAuth(), fakeRepoClient{err: want}, localrepo.NewLocalRepo()),
					Connector: fakeConnector{},
				}
			},
			tool: "nipa_log",
		},
		{
			name: "branch list",
			uc: func() UseCases {
				return UseCases{
					Repo:      usecase.NewRepo(loggedInAuth(), fakeRepoClient{err: want}, localrepo.NewLocalRepo()),
					Connector: fakeConnector{},
				}
			},
			tool: "nipa_branch_list",
		},
		{
			name: "mr list",
			uc: func() UseCases {
				return UseCases{MR: usecase.NewMergeRequest(loggedInAuth(), fakeMRClient{err: want}, localrepo.NewLocalRepo())}
			},
			tool: "nipa_mr_list",
		},
		{
			name: "lock list",
			uc: func() UseCases {
				return UseCases{Lock: usecase.NewFileLock(loggedInAuth(), fakeLockClient{listErr: want}, localrepo.NewLocalRepo())}
			},
			tool: "nipa_lock_list",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := connect(t, Options{NewUseCases: tc.uc})
			res := call(t, session, tc.tool, map[string]any{"repo": root})
			require.True(t, res.IsError)
			require.Contains(t, toolText(t, res), "server exploded")
		})
	}
}

func TestDiff_MoreValidation(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "a\n"})
	session := connect(t, Options{NewUseCases: offlineUseCases})

	res := call(t, session, "nipa_diff", map[string]any{"repo": root, "merge_base": true})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "merge_base requires two revisions")

	negative := -1
	res = call(t, session, "nipa_diff", map[string]any{"repo": root, "unified": negative})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "unified must not be negative")

	res = call(t, session, "nipa_diff", map[string]any{"repo": filepath.Join(t.TempDir(), "missing")})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "not a nipa repository")
}

func TestWorkingCopy_InitError(t *testing.T) {
	root := setupRepo(t, nil)
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".nipa", "objects")))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".nipa", "objects"), []byte("file"), 0o644))

	readSession := connect(t, Options{NewUseCases: offlineUseCases})
	res := call(t, readSession, "nipa_status", map[string]any{"repo": root})
	require.True(t, res.IsError)

	writeSession := connect(t, Options{AllowWrite: true, NewUseCases: offlineUseCases})
	res = call(t, writeSession, "nipa_add", map[string]any{"repo": root, "paths": []string{"a.txt"}})
	require.True(t, res.IsError)
}

func TestAdd_PathError(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{AllowWrite: true, NewUseCases: offlineUseCases})

	res := call(t, session, "nipa_add", map[string]any{"repo": root, "paths": []string{"missing.txt"}})
	require.True(t, res.IsError)
}

func TestPush_RepoRootError(t *testing.T) {
	session := connect(t, Options{AllowWrite: true, NewUseCases: func() UseCases {
		return UseCases{Push: usecase.NewPush(nil, nil, localrepo.NewLocalRepo())}
	}})

	res := call(t, session, "nipa_push", map[string]any{"repo": filepath.Join(t.TempDir(), "missing"), "message": "m"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "not a nipa repository")
}
