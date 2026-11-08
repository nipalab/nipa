package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeLLM struct {
	mu      sync.Mutex
	answer  string
	status  int
	prompts []string
}

func (f *fakeLLM) handler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	for _, m := range req.Messages {
		f.prompts = append(f.prompts, m.Content)
	}
	f.mu.Unlock()
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, `{"error":{"message":"model overloaded"}}`)
		return
	}
	body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": f.answer}}}})
	_, _ = w.Write(body)
}

func (f *fakeLLM) userPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prompts) < 2 {
		return ""
	}
	return f.prompts[len(f.prompts)-1]
}

func withLLM(t *testing.T, answer string) *fakeLLM {
	t.Helper()
	f := &fakeLLM{answer: answer}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(config.EnvLLMEndpoint, srv.URL+"/v1")
	t.Setenv(config.EnvLLMModel, "test-model")
	t.Setenv(config.EnvLLMAPIKey, "")
	return f
}

func noLLM(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(config.EnvLLMEndpoint, "")
	t.Setenv(config.EnvLLMModel, "")
}

func newGeneratePushCli(t *testing.T, client *fakePushClient, tty bool) *Cli {
	t.Helper()
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	c := NewCli(&fakeUsecaseContainer{
		push: usecase.NewPush(auth, client, localrepo.NewLocalRepo()),
		diff: usecase.NewDiff(nil, nil, localrepo.NewLocalRepo()),
	}, &fakeConnector{})
	c.terminal = func(io.Reader) bool { return tty }
	return c
}

func runWithInput(t *testing.T, dir string, cmd *cobra.Command, input string, args ...string) (string, error) {
	t.Helper()
	cmd.SetIn(strings.NewReader(input))
	return runCmdInDir(t, dir, cmd, args...)
}

func stagedRepo(t *testing.T) string {
	t.Helper()
	root := setupRepo(t, "main")
	writeFile(t, root, "a.txt", "hello world\n")
	writeFile(t, root, "tex.bin", "\x00\x01\x02PNGSECRET")
	stagePath(t, root, "a.txt")
	stagePath(t, root, "tex.bin")
	return root
}

func TestPushGenerate_Yes(t *testing.T) {
	llm := withLLM(t, "```\nfeat: add greeting\n\nAdds a.txt.\n```")
	root := stagedRepo(t)
	client := &fakePushClient{}
	out, err := runCmdInDir(t, root, newGeneratePushCli(t, client, false).setupPushCmd(), "--generate", "--yes")
	require.NoError(t, err)
	require.Equal(t, "feat: add greeting\n\nAdds a.txt.\n\nNipa-Generated: test-model", client.message)
	require.Contains(t, out, "Generated commit message:")
	prompt := llm.userPrompt()
	require.Contains(t, prompt, "A a.txt")
	require.Contains(t, prompt, "+hello world")
	require.Contains(t, prompt, "tex.bin [binary]")
	require.NotContains(t, prompt, "PNGSECRET", "binary bytes must never reach the LLM")
}

func TestPushGenerate_Confirm(t *testing.T) {
	withLLM(t, "fix: thing")
	root := stagedRepo(t)
	client := &fakePushClient{}
	_, err := runWithInput(t, root, newGeneratePushCli(t, client, true).setupPushCmd(), "y\n", "--generate")
	require.NoError(t, err)
	require.Equal(t, "fix: thing\n\nNipa-Generated: test-model", client.message)

	root = stagedRepo(t)
	client = &fakePushClient{}
	_, err = runWithInput(t, root, newGeneratePushCli(t, client, true).setupPushCmd(), "n\n", "--generate")
	require.ErrorContains(t, err, "cancelled")
	require.Empty(t, client.message)
}

func TestPushGenerate_NonInteractive(t *testing.T) {
	withLLM(t, "fix: thing")
	root := stagedRepo(t)
	client := &fakePushClient{}
	_, err := runCmdInDir(t, root, newGeneratePushCli(t, client, false).setupPushCmd(), "--generate")
	require.ErrorContains(t, err, "pass --yes")
	require.Empty(t, client.message)
}

func TestPushGenerate_DryRun(t *testing.T) {
	llm := withLLM(t, "chore: update")
	root := stagedRepo(t)
	client := &fakePushClient{}
	out, err := runCmdInDir(t, root, newGeneratePushCli(t, client, false).setupPushCmd(), "--generate", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "generated commit message:\n\nchore: update\n\nNipa-Generated: test-model")
	require.Empty(t, client.message)
	require.NotEmpty(t, llm.userPrompt())

	out, err = runCmdInDir(t, root, newGeneratePushCli(t, client, false).setupPushCmd(), "--generate", "--dry-run", "--json")
	require.NoError(t, err)
	var plan struct {
		GeneratedMessage string `json:"generated_message"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &plan))
	require.Equal(t, "chore: update\n\nNipa-Generated: test-model", plan.GeneratedMessage)
}

func TestPushGenerate_Errors(t *testing.T) {
	root := stagedRepo(t)
	client := &fakePushClient{}

	noLLM(t)
	_, err := runCmdInDir(t, root, newGeneratePushCli(t, client, false).setupPushCmd(), "--generate", "--yes")
	require.ErrorContains(t, err, "set NIPA_LLM_ENDPOINT and NIPA_LLM_MODEL")

	_, err = runCmdInDir(t, root, newGeneratePushCli(t, client, false).setupPushCmd(), "--generate", "-m", "x")
	require.ErrorContains(t, err, "none of the others can be")

	llm := withLLM(t, "")
	_, err = runCmdInDir(t, root, newGeneratePushCli(t, client, false).setupPushCmd(), "--generate", "--yes")
	require.ErrorContains(t, err, "no completion")

	llm.status = http.StatusServiceUnavailable
	_, err = runCmdInDir(t, root, newGeneratePushCli(t, client, false).setupPushCmd(), "--generate", "--yes")
	require.ErrorContains(t, err, "answered 503: model overloaded")

	withLLM(t, "```\n```")
	_, err = runCmdInDir(t, root, newGeneratePushCli(t, client, false).setupPushCmd(), "--generate", "--yes")
	require.ErrorContains(t, err, "empty answer")

	withLLM(t, "x")
	empty := setupRepo(t, "main")
	_, err = runCmdInDir(t, empty, newGeneratePushCli(t, client, false).setupPushCmd(), "--generate", "--yes")
	require.ErrorContains(t, err, "nothing staged")

	noDiff := NewCli(&fakeUsecaseContainer{push: newGeneratePushCli(t, client, false).useCase.Push()}, &fakeConnector{})
	_, err = runCmdInDir(t, root, noDiff.setupPushCmd(), "--generate", "--yes")
	require.ErrorContains(t, err, "diff is not configured")
	require.Empty(t, client.message)
}

type fakeRevDiffClient struct {
	branches map[string]string
	commits  map[string]map[string]string
	base     string
}

func (f *fakeRevDiffClient) Connect(context.Context, string) error { return nil }

func (f *fakeRevDiffClient) GetBranchByName(_ context.Context, _, _, name string) (*serverDomain.Branch, error) {
	id, ok := f.branches[name]
	if !ok {
		return nil, &domain.Error{Code: 404, Message: "branch not found"}
	}
	parsed, err := snow.ParseBase36(id)
	if err != nil {
		return nil, err
	}
	return &serverDomain.Branch{Name: name, CommitID: &parsed}, nil
}

func (f *fakeRevDiffClient) GetTagByName(context.Context, string, string, string) (*domain.Tag, error) {
	return nil, &domain.Error{Code: 404, Message: "tag not found"}
}

func (f *fakeRevDiffClient) GetCommit(_ context.Context, _, _, id string) (*domain.CommitDetail, error) {
	files, ok := f.commits[id]
	if !ok {
		return nil, &domain.Error{Code: 404, Message: "commit not found"}
	}
	return &domain.CommitDetail{ID: id, Tree: revTree(files)}, nil
}

func (f *fakeRevDiffClient) GetMergeBase(context.Context, string, string, domain.MergeRef, domain.MergeRef) (*domain.MergeBaseInfo, error) {
	return &domain.MergeBaseInfo{MergeBaseCommitID: f.base, MergeBaseTree: revTree(f.commits[f.base])}, nil
}

func (f *fakeRevDiffClient) DownloadChunks(_ context.Context, _ domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(serverDomain.Hash, []byte) error) error {
	all := map[serverDomain.Hash][]byte{}
	for _, files := range f.commits {
		for _, content := range files {
			chunks, _ := chunker.ChunkAll([]byte(content))
			for _, c := range chunks {
				all[c.Hash] = c.Data
			}
		}
	}
	for _, h := range hashes {
		if data, ok := all[h]; ok {
			if err := onChunk(h, data); err != nil {
				return err
			}
		}
	}
	return nil
}

func revTree(files map[string]string) *serverDomain.TreeNode {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	node := &serverDomain.TreeNode{}
	for _, name := range names {
		content := []byte(files[name])
		chunks, _ := chunker.ChunkAll(content)
		hashes := make([]serverDomain.Hash, len(chunks))
		fileChunks := make([]serverDomain.Chunk, len(chunks))
		for i, c := range chunks {
			hashes[i] = c.Hash
			fileChunks[i] = serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))}
		}
		node.FileChildren = append(node.FileChildren, &serverDomain.File{
			Name: name, Mode: 2, SizeBytes: int64(len(content)), IsBinary: chunker.IsBinary(content),
			Hash: chunker.FileHash(hashes), Chunks: fileChunks,
		})
	}
	return node
}

type logRepo struct {
	fakeListRepoInterface
	log []*serverDomain.CommitLogEntry
}

func (l logRepo) GetCommitLog(context.Context, string, string, string, *snow.ID, int) ([]*serverDomain.CommitLogEntry, error) {
	return l.log, nil
}

func newGenerateMRCli(t *testing.T, client *fakeMRClient) *Cli {
	t.Helper()
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	diffClient := &fakeRevDiffClient{
		branches: map[string]string{"main": "a", "feature": "b"},
		commits: map[string]map[string]string{
			"a": {"a.txt": "one\n"},
			"b": {"a.txt": "one\ntwo\n", "c.txt": "c\n"},
		},
		base: "a",
	}
	repo := logRepo{
		fakeListRepoInterface: fakeListRepoInterface{branches: []*serverDomain.Branch{{Name: "main", IsDefault: true}}},
		log:                   []*serverDomain.CommitLogEntry{{Commit: serverDomain.Commit{ID: 11, Message: "add c"}}, {Commit: serverDomain.Commit{ID: 10, Message: "base"}}},
	}
	c := NewCli(&fakeUsecaseContainer{
		mr:   usecase.NewMergeRequest(auth, client, localrepo.NewLocalRepo()),
		diff: usecase.NewDiff(auth, diffClient, localrepo.NewLocalRepo()),
		repo: usecase.NewRepo(auth, &repo, localrepo.NewLocalRepo()),
	}, &fakeConnector{})
	c.terminal = func(io.Reader) bool { return false }
	return c
}

func TestMrCreateGenerate(t *testing.T) {
	llm := withLLM(t, "# Add c and extend a\n\nSummary.\n\n- c.txt added")
	root := setupRepo(t, "feature")
	client := &fakeMRClient{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		createResult:  &domain.MergeRequest{Number: 4, SourceBranch: "feature", TargetBranch: "main", Title: "Add c and extend a"},
	}
	out, err := runCmdInDir(t, root, newGenerateMRCli(t, client).setupMrCmd(), "create", "--generate", "--yes")
	require.NoError(t, err)
	require.Equal(t, "Add c and extend a", client.createTitle)
	require.Equal(t, "Summary.\n\n- c.txt added\n\nNipa-Generated: test-model", client.createDescription)
	require.Equal(t, "feature", client.createSource)
	require.Equal(t, "main", client.createTarget)
	require.Contains(t, out, "Generated merge request:\n\nTitle: Add c and extend a")
	require.Contains(t, out, "Merge request #4 opened")
	prompt := llm.userPrompt()
	require.Contains(t, prompt, `from "feature" into "main"`)
	require.Contains(t, prompt, "- add c\n")
	require.Contains(t, prompt, "+two")

	client = &fakeMRClient{createResult: &domain.MergeRequest{Number: 5, Title: "Mine"}}
	_, err = runCmdInDir(t, root, newGenerateMRCli(t, client).setupMrCmd(), "create", "--generate", "--yes", "--title", "Mine", "--target", "main")
	require.NoError(t, err)
	require.Equal(t, "Mine", client.createTitle)
}

func TestMrCreateGenerate_Errors(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{createResult: &domain.MergeRequest{Number: 1}}

	_, err := runCmdInDir(t, root, newGenerateMRCli(t, client).setupMrCmd(), "create", "--generate", "-d", "x")
	require.ErrorContains(t, err, "none of the others can be")

	noLLM(t)
	_, err = runCmdInDir(t, root, newGenerateMRCli(t, client).setupMrCmd(), "create", "--generate")
	require.ErrorContains(t, err, "no LLM endpoint configured")

	withLLM(t, "Title\n\nBody")
	_, err = runCmdInDir(t, root, newGenerateMRCli(t, client).setupMrCmd(), "create", "--generate")
	require.ErrorContains(t, err, "pass --yes")

	_, err = runCmdInDir(t, root, newGenerateMRCli(t, client).setupMrCmd(), "create", "--generate", "--yes", "--target", "feature")
	require.ErrorContains(t, err, "must differ")

	noDiff := newMRCli(t, client)
	_, err = runCmdInDir(t, root, noDiff.setupMrCmd(), "create", "--generate", "--yes")
	require.ErrorContains(t, err, "diff is not configured")

	withLLM(t, "  ")
	_, err = runCmdInDir(t, root, newGenerateMRCli(t, client).setupMrCmd(), "create", "--generate", "--yes", "--target", "main")
	require.ErrorContains(t, err, "no completion")
	require.Empty(t, client.createTitle)
}

func TestConfirm(t *testing.T) {
	var out bytes.Buffer
	for input, want := range map[string]bool{"y\n": true, "YES\n": true, "n\n": false, "\n": false, "y": true, "": false} {
		ok, err := confirm(strings.NewReader(input), &out, "? ")
		require.NoError(t, err)
		require.Equal(t, want, ok, input)
	}
	_, err := confirm(strings.NewReader("y"), failingWriter{}, "? ")
	require.Error(t, err)
	_, err = confirm(errReader{}, &out, "? ")
	require.Error(t, err)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestInteractive(t *testing.T) {
	c := &Cli{}
	require.False(t, c.interactive(strings.NewReader("")))
	f, err := os.CreateTemp(t.TempDir(), "in")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	require.False(t, c.interactive(f))
	c.terminal = func(io.Reader) bool { return true }
	require.True(t, c.interactive(nil))
}
