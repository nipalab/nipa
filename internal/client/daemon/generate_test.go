package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/aiprompt"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/generate"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type stubModel struct {
	answer string
	err    error
}

func (m *stubModel) Complete(context.Context, aiprompt.Prompt) (string, error) {
	return m.answer, m.err
}
func (m *stubModel) Model() string { return "stub" }

type stubGenerate struct {
	model    *stubModel
	modelErr error
	inputErr error
	calls    []string
}

func (g *stubGenerate) Model() (generate.LLM, error) {
	if g.modelErr != nil {
		return nil, g.modelErr
	}
	return g.model, nil
}

func (g *stubGenerate) CommitInput(_ context.Context, root string) (*aiprompt.CommitInput, error) {
	g.calls = append(g.calls, "commit:"+root)
	return &aiprompt.CommitInput{Branch: "main"}, g.inputErr
}

func (g *stubGenerate) MRInput(_ context.Context, root, source, target string) (*aiprompt.MRInput, error) {
	g.calls = append(g.calls, "mr:"+source+">"+target)
	return &aiprompt.MRInput{Source: "feature", Target: "main"}, g.inputErr
}

func generateServer(t *testing.T, runner GenerateRunner) (*Server, string) {
	t.Helper()
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Generate: runner})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root
}

func TestServer_GenerateCommit(t *testing.T) {
	stub := &stubGenerate{model: &stubModel{answer: "feat: add a"}}
	srv, root := generateServer(t, SerialGenerator(stub))
	res, err := srv.Generate(context.Background(), &daemonpb.GenerateRequest{Root: root, Kind: "commit"})
	require.NoError(t, err)
	assert.Equal(t, "feat: add a", res.GetText())
	assert.Equal(t, "stub", res.GetModel())
	assert.Empty(t, res.GetTitle())
	assert.Equal(t, []string{"commit:" + root}, stub.calls)
}

func TestServer_GenerateMergeRequest(t *testing.T) {
	stub := &stubGenerate{model: &stubModel{answer: "Add c\n\nBody"}}
	srv, root := generateServer(t, SerialGenerator(stub))
	res, err := srv.Generate(context.Background(), &daemonpb.GenerateRequest{Root: root, Kind: "mr", Source: "feature"})
	require.NoError(t, err)
	assert.Equal(t, "Add c", res.GetTitle())
	assert.Equal(t, "Body", res.GetText())
	assert.Equal(t, "feature", res.GetSource())
	assert.Equal(t, "main", res.GetTarget())
	assert.Equal(t, []string{"mr:feature>"}, stub.calls)
}

func TestServer_GenerateErrors(t *testing.T) {
	stub := &stubGenerate{model: &stubModel{answer: "x"}}
	srv, root := generateServer(t, stub)

	_, err := srv.Generate(context.Background(), &daemonpb.GenerateRequest{Root: root, Kind: "review"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	stub.modelErr = errors.Join(generate.ErrNotConfigured)
	_, err = srv.Generate(context.Background(), &daemonpb.GenerateRequest{Root: root, Kind: "commit"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Contains(t, status.Convert(err).Message(), "Settings > AI assistance")
	assert.Empty(t, stub.calls)

	stub.modelErr = nil
	stub.inputErr = clientDomain.NewUserError("nothing staged to describe")
	_, err = srv.Generate(context.Background(), &daemonpb.GenerateRequest{Root: root, Kind: "commit"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "nothing staged")

	stub.inputErr = nil
	stub.model.err = clientDomain.NewUserError("LLM endpoint answered 401: bad key")
	_, err = srv.Generate(context.Background(), &daemonpb.GenerateRequest{Root: root, Kind: "mr"})
	assert.Contains(t, err.Error(), "answered 401")

	stub.model.err = nil
	stub.model.answer = ""
	_, err = srv.Generate(context.Background(), &daemonpb.GenerateRequest{Root: root, Kind: "commit"})
	assert.Contains(t, err.Error(), "empty answer")

	none, noneRoot := generateServer(t, nil)
	_, err = none.Generate(context.Background(), &daemonpb.GenerateRequest{Root: noneRoot, Kind: "commit"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))

	_, err = none.Generate(context.Background(), &daemonpb.GenerateRequest{Root: t.TempDir(), Kind: "commit"})
	assert.Error(t, err)
}
