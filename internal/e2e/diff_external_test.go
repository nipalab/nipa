package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/cli"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
)

// helperCall is one invocation record written by the TestHelperDiffTool
// child process acting as the external diff tool.
type helperCall struct {
	Argv     []string `json:"argv"`
	FullArgv []string `json:"full_argv"`
	Path     string   `json:"path"`
	Status   string   `json:"status"`
	OldB64   string   `json:"old_b64"`
	NewB64   string   `json:"new_b64"`
	OldDir   string   `json:"old_dir"`
	NewDir   string   `json:"new_dir"`
}

// TestHelperDiffTool is not a real test: it is the entrypoint for the
// child process re-executed as the external diff tool. It records what it
// received and exits with NIPA_E2E_EXIT (default 1, proving the CLI
// tolerates tool exit codes).
func TestHelperDiffTool(t *testing.T) {
	if os.Getenv("NIPA_E2E_DIFFTOOL") != "1" {
		t.Skip("diff tool helper entrypoint")
	}
	var argv []string
	for i, a := range os.Args {
		if a == "--" {
			argv = os.Args[i+1:]
			break
		}
	}
	if len(argv) < 2 {
		os.Exit(2)
	}
	oldData, _ := os.ReadFile(argv[0])
	newData, _ := os.ReadFile(argv[1])
	rec := helperCall{
		Argv:     argv,
		FullArgv: os.Args,
		Path:     os.Getenv("NIPA_PATH"),
		Status:   os.Getenv("NIPA_STATUS"),
		OldB64:   base64.StdEncoding.EncodeToString(oldData),
		NewB64:   base64.StdEncoding.EncodeToString(newData),
		OldDir:   os.Getenv("NIPA_LOCAL_DIR"),
		NewDir:   os.Getenv("NIPA_REMOTE_DIR"),
	}
	out := os.Getenv("NIPA_E2E_OUT")
	f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		os.Exit(2)
	}
	_ = json.NewEncoder(f).Encode(rec)
	_ = f.Close()
	code := 1
	if v := os.Getenv("NIPA_E2E_EXIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			code = n
		}
	}
	os.Exit(code)
}

// e2eCliRegistry wires every client usecase for driving the real CLI.
type e2eCliRegistry struct {
	auth   *clientusecase.Auth
	repo   *clientusecase.Repo
	push   *clientusecase.Push
	update *clientusecase.Update
	merge  *clientusecase.Merge
	diff   *clientusecase.Diff
}

func (r *e2eCliRegistry) Auth() *clientusecase.Auth     { return r.auth }
func (r *e2eCliRegistry) Repo() *clientusecase.Repo     { return r.repo }
func (r *e2eCliRegistry) Push() *clientusecase.Push     { return r.push }
func (r *e2eCliRegistry) Update() *clientusecase.Update { return r.update }
func (r *e2eCliRegistry) Merge() *clientusecase.Merge   { return r.merge }
func (r *e2eCliRegistry) Diff() *clientusecase.Diff     { return r.diff }

func TestEndToEnd_DiffExternalTool(t *testing.T) {
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	grpcClient := clientgrpc.NewClient(transport, session)
	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})

	require.NoError(t, grpcClient.Connect(ctx, host))
	loginResult, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(loginResult))

	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", "", target))

	writeFile(t, target, "a.txt", "v1\n")
	stagePath(t, target, "a.txt")
	require.NoError(t, pusher.Run(ctx, target, "first"))

	imgV2 := "PNG\x00binary-v2"
	writeFile(t, target, "a.txt", "v2\n")
	writeFile(t, target, "img.bin", imgV2)
	stagePath(t, target, "a.txt")
	stagePath(t, target, "img.bin")
	require.NoError(t, pusher.Run(ctx, target, "second"))

	entries, err := repo.Log(ctx, host, e2eOrgSlug, e2eProjectSlug, "main")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	hash1 := entries[1].Hash.String()
	hash2 := entries[0].Hash.String()

	reg := &e2eCliRegistry{
		auth:   auth,
		repo:   repo,
		push:   pusher,
		update: clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo()),
		merge:  clientusecase.NewMerge(auth, grpcClient, localrepo.NewLocalRepo(), pusher),
		diff:   clientusecase.NewDiff(auth, grpcClient, localrepo.NewLocalRepo()),
	}
	c := cli.NewCli(reg, grpcClient)

	// the helper template re-executes this test binary as the tool;
	// quoting the binary path exercises quoted template parsing too.
	self, err := os.Executable()
	require.NoError(t, err)
	toolTmpl := `"` + self + `" -test.run TestHelperDiffTool -- $LOCAL $REMOTE`

	callsFile := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("NIPA_E2E_DIFFTOOL", "1")
	t.Setenv("NIPA_E2E_OUT", callsFile)
	t.Setenv("NIPA_E2E_EXIT", "")
	t.Setenv("NIPA_DIFF_TOOL", "")

	oldArgs := os.Args
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	defer func() {
		os.Args = oldArgs
		_ = os.Chdir(oldWd)
	}()
	require.NoError(t, os.Chdir(target))

	runCLI := func(args ...string) {
		t.Helper()
		os.Args = append([]string{"nipa"}, args...)
		require.NoError(t, c.Run(), "tool exit 1 must be tolerated")
	}
	readCalls := func() []helperCall {
		t.Helper()
		data, err := os.ReadFile(callsFile)
		require.NoError(t, err)
		var calls []helperCall
		for _, line := range nonEmptyLines(string(data)) {
			var hc helperCall
			require.NoError(t, json.Unmarshal([]byte(line), &hc))
			calls = append(calls, hc)
		}
		return calls
	}
	clearCalls := func() {
		t.Helper()
		require.NoError(t, os.Remove(callsFile))
	}
	decode := func(b64 string) string {
		t.Helper()
		raw, err := base64.StdEncoding.DecodeString(b64)
		require.NoError(t, err)
		return string(raw)
	}

	// revision vs revision through the real CLI and a real child process:
	// text modification plus a binary add (exercises chunk download for
	// binaries over DownloadChunks).
	runCLI("diff", hash1, hash2, "--tool", toolTmpl)
	calls := readCalls()
	require.Len(t, calls, 2)
	require.Equal(t, "a.txt", calls[0].Path)
	require.Equal(t, "M", calls[0].Status)
	require.Equal(t, "v1\n", decode(calls[0].OldB64))
	require.Equal(t, "v2\n", decode(calls[0].NewB64))
	require.Equal(t, self, calls[0].FullArgv[0])
	require.Contains(t, calls[0].FullArgv, "-test.run")
	require.Equal(t, "img.bin", calls[1].Path)
	require.Equal(t, "A", calls[1].Status)
	require.Equal(t, "", decode(calls[1].OldB64))
	require.Equal(t, imgV2, decode(calls[1].NewB64))
	require.True(t, strings.HasSuffix(calls[0].OldDir, string(filepath.Separator)+"old"), calls[0].OldDir)
	require.True(t, strings.HasSuffix(calls[0].NewDir, string(filepath.Separator)+"new"), calls[0].NewDir)
	require.NoDirExists(t, calls[0].OldDir, "materialization temp dir must be cleaned up")
	clearCalls()

	// revision vs working copy picks up uncommitted edits
	writeFile(t, target, "a.txt", "v3-local\n")
	runCLI("diff", hash1, "--tool", toolTmpl)
	calls = readCalls()
	require.Len(t, calls, 1)
	require.Equal(t, "a.txt", calls[0].Path)
	require.Equal(t, "v1\n", decode(calls[0].OldB64))
	require.Equal(t, "v3-local\n", decode(calls[0].NewB64))
	clearCalls()

	// --external resolves the template from the environment
	t.Setenv("NIPA_DIFF_TOOL", toolTmpl)
	runCLI("diff", "--external")
	calls = readCalls()
	require.Len(t, calls, 1)
	require.Equal(t, "a.txt", calls[0].Path)
	require.Equal(t, "v2\n", decode(calls[0].OldB64))
	require.Equal(t, "v3-local\n", decode(calls[0].NewB64))
}

func nonEmptyLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}
