package difftool

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeScript(t *testing.T, body string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "tool.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	return script
}

func TestRun_InvokesCommandWithGitArguments(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	oldOut := filepath.Join(dir, "old.txt")
	newOut := filepath.Join(dir, "new.txt")
	script := writeScript(t, `printf '%s\n' "$@" >> "$ARGS_FILE"
cat "$2" > "$OLD_OUT"
cat "$5" > "$NEW_OUT"`)
	t.Setenv("ARGS_FILE", argsFile)
	t.Setenv("OLD_OUT", oldOut)
	t.Setenv("NEW_OUT", newOut)

	pairs := []FilePair{{
		Path: "a.txt", Old: []byte("old"), New: []byte("new"),
		OldHash: "aa", NewHash: "bb", OldMode: "100644", NewMode: "100755",
	}}
	require.NoError(t, Run(context.Background(), script, pairs, io.Discard, io.Discard))

	raw, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	args := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	require.Len(t, args, 7)
	require.Equal(t, "a.txt", args[0])
	require.Equal(t, "aa", args[2])
	require.Equal(t, "100644", args[3])
	require.Equal(t, "bb", args[5])
	require.Equal(t, "100755", args[6])

	oldContent, err := os.ReadFile(oldOut)
	require.NoError(t, err)
	require.Equal(t, "old", string(oldContent))
	newContent, err := os.ReadFile(newOut)
	require.NoError(t, err)
	require.Equal(t, "new", string(newContent))
}

func TestRun_MissingSideUsesDevNull(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	script := writeScript(t, `printf '%s\n' "$@" >> "$ARGS_FILE"`)
	t.Setenv("ARGS_FILE", argsFile)

	pair := FilePair{Path: "new.txt", New: []byte("new"), NewHash: "bb", NewMode: "100644", OldMissing: true, OldHash: strings.Repeat("0", 64), OldMode: "000000"}
	require.NoError(t, Run(context.Background(), script, []FilePair{pair}, io.Discard, io.Discard))

	raw, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	args := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	require.Equal(t, os.DevNull, args[1])
	require.Equal(t, "000000", args[3])
}

func TestRun_ToleratesExitOne(t *testing.T) {
	script := writeScript(t, "exit 1")
	pair := FilePair{Path: "a.txt"}
	require.NoError(t, Run(context.Background(), script, []FilePair{pair}, io.Discard, io.Discard))
}

func TestRun_ErrorsOnExitTwo(t *testing.T) {
	script := writeScript(t, "exit 2")
	pair := FilePair{Path: "a.txt"}
	err := Run(context.Background(), script, []FilePair{pair}, io.Discard, io.Discard)
	require.ErrorContains(t, err, "external diff failed for a.txt")
}

func TestRun_EmptyCommand(t *testing.T) {
	err := Run(context.Background(), "  ", nil, io.Discard, io.Discard)
	require.ErrorContains(t, err, "empty")
}

func TestRun_NoPairs(t *testing.T) {
	require.NoError(t, Run(context.Background(), "/does/not/exist", nil, io.Discard, io.Discard))
}
