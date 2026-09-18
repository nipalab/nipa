package difftool

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubRunner struct {
	calls []runnerCall
	errs  []error
}

type runnerCall struct {
	argv []string
	env  []string
}

func (s *stubRunner) Run(_ context.Context, argv, env []string) error {
	s.calls = append(s.calls, runnerCall{argv: argv, env: env})
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		return err
	}
	return nil
}

func testPairs() ([]Pair, *Tree) {
	return []Pair{
			{Path: "a.txt", Status: "M", Local: "/o/a.txt", Remote: "/n/a.txt"},
			{Path: "b.txt", Status: "A", Local: "/o/b.txt", Remote: "/n/b.txt"},
		}, &Tree{
			Dir:    "/tmp",
			OldDir: "/o",
			NewDir: "/n",
		}
}

func TestRun(t *testing.T) {
	tmpl, err := Parse("tool --left $LOCAL --right $REMOTE")
	require.NoError(t, err)
	stub := &stubRunner{}
	pairs, tree := testPairs()
	var progress bytes.Buffer

	require.NoError(t, Run(context.Background(), stub, tmpl, tree, pairs, &progress))
	require.Len(t, stub.calls, 2)
	require.Equal(t, []string{"tool", "--left", "/o/a.txt", "--right", "/n/a.txt"}, stub.calls[0].argv)
	require.Equal(t, []string{"tool", "--left", "/o/b.txt", "--right", "/n/b.txt"}, stub.calls[1].argv)
	require.Contains(t, stub.calls[0].env, "NIPA_PATH=a.txt")
	require.Contains(t, stub.calls[0].env, "NIPA_STATUS=M")
	require.Equal(t, "Viewing (1/2): 'a.txt'\nViewing (2/2): 'b.txt'\n", progress.String())
}

func TestRun_Empty(t *testing.T) {
	tmpl, err := Parse("tool $LOCAL")
	require.NoError(t, err)
	stub := &stubRunner{}
	_, tree := testPairs()

	require.NoError(t, Run(context.Background(), stub, tmpl, tree, nil, nil))
	require.Empty(t, stub.calls)
}

func TestRun_ExitCodeTolerated(t *testing.T) {
	tmpl, err := Parse("tool $LOCAL $REMOTE")
	require.NoError(t, err)
	// a compare tool exits nonzero when files differ; that must not stop
	// the run or surface as an error.
	stub := &stubRunner{errs: []error{&exec.ExitError{}}}
	pairs, tree := testPairs()

	require.NoError(t, Run(context.Background(), stub, tmpl, tree, pairs, nil))
	require.Len(t, stub.calls, 2)
}

func TestRun_LaunchFailureAborts(t *testing.T) {
	tmpl, err := Parse("tool $LOCAL")
	require.NoError(t, err)
	wantErr := errors.New("boom")
	stub := &stubRunner{errs: []error{wantErr}}
	pairs, tree := testPairs()

	err = Run(context.Background(), stub, tmpl, tree, pairs, nil)
	require.ErrorIs(t, err, wantErr)
	require.Len(t, stub.calls, 1, "later files must not run after a launch failure")
}

func TestExecRunner_Empty(t *testing.T) {
	err := ExecRunner{}.Run(context.Background(), nil, nil)
	require.Error(t, err)
}

func TestExecRunner_Success(t *testing.T) {
	// Helper-process trick: re-exec the test binary so a real process
	// runs on every platform. The child signals via a marker file.
	if os.Getenv("NIPA_DIFFTOOL_HELPER") == "1" {
		out := os.Getenv("NIPA_DIFFTOOL_OUT")
		args := strings.Join(os.Args[1:], " ")
		if err := os.WriteFile(out, []byte(args), 0o644); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	out := filepath.Join(t.TempDir(), "helper.out")
	t.Setenv("NIPA_DIFFTOOL_HELPER", "1")
	t.Setenv("NIPA_DIFFTOOL_OUT", out)

	self, err := os.Executable()
	require.NoError(t, err)
	err = ExecRunner{}.Run(context.Background(),
		[]string{self, "-test.run", "TestExecRunner_Success", "--", "hello"},
		[]string{"NIPA_DIFFTOOL_MARKER=1"})
	require.NoError(t, err)

	data, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Contains(t, string(data), "hello")
}

func TestExecRunner_NotFound(t *testing.T) {
	err := ExecRunner{}.Run(context.Background(), []string{"nipa-definitely-missing-tool-xyz"}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}
