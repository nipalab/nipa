package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	clientgitimport "github.com/nipalab/nipa/internal/client/gitimport"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/gitimport"
)

type fakeGitImporter struct {
	opts     clientgitimport.Options
	sum      *clientgitimport.Summary
	err      error
	progress clientgitimport.Progress
}

func (f *fakeGitImporter) Run(_ context.Context, opts clientgitimport.Options, progress clientgitimport.Progress) (*clientgitimport.Summary, error) {
	f.opts, f.progress = opts, progress
	if f.err != nil {
		return nil, f.err
	}
	if !opts.DryRun && opts.Confirm != nil {
		if err := opts.Confirm(f.sum); err != nil {
			return f.sum, err
		}
	}
	if progress != nil {
		progress.ImportStart(f.sum.Commits)
		progress.ImportProgress(f.sum.Commits, f.sum.UploadedBytes)
		progress.ImportEnd()
	}
	return f.sum, nil
}

func importSummary() *clientgitimport.Summary {
	return &clientgitimport.Summary{
		Target:        "org/project",
		Commits:       3,
		Known:         1,
		Imported:      3,
		UploadedBytes: 2048,
		Branches:      2,
		Tags:          1,
		DefaultBranch: "main",
		Refs: []clientgitimport.RefResult{
			{Kind: "branch", Name: "main", Status: clientgitimport.RefCreated, CommitID: "a1"},
			{Kind: "branch", Name: "feature", Status: clientgitimport.RefDiverged, Message: "the branch has commits the import does not contain"},
			{Kind: "tag", Name: "v1", Status: clientgitimport.RefCreated, Message: "note"},
		},
		Warnings: []gitimport.Warning{{Kind: gitimport.WarnOctopus, Commit: "0123456789abcdef", Message: "octopus merge"}},
		Report:   &gitimport.Report{Commits: 3, Blobs: 2, Bytes: 4096, Files: 4, Removed: 1},
	}
}

type importRun struct {
	stdin    string
	terminal bool
	conn     *fakeConnector
}

func runImportCmd(t *testing.T, imp *fakeGitImporter, run importRun, args ...string) (string, string, error) {
	t.Helper()
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	conn := run.conn
	if conn == nil {
		conn = &fakeConnector{}
	}
	c := NewCli(&fakeUsecaseContainer{auth: auth, gitImport: imp}, conn)
	c.terminal = func(io.Reader) bool { return run.terminal }
	cmd := c.setupImportCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(run.stdin))
	cmd.SetArgs(append([]string{"git"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

const importURL = "http://example.com/org/project"

func TestImportGitCmd_TextWithWarnings(t *testing.T) {
	imp := &fakeGitImporter{sum: importSummary()}
	conn := &fakeConnector{}
	out, _, err := runImportCmd(t, imp, importRun{conn: conn}, "../game", importURL, "--yes",
		"--refs", "refs/heads/*", "--default-branch", "main", "--symlinks", "file", "--source-key", "game", "--update", "--reset-state")
	require.NoError(t, err)
	require.Equal(t, "http://example.com", conn.lastHost)
	require.Equal(t, "../game", imp.opts.GitPath)
	require.Equal(t, "example.com", imp.opts.Host)
	require.Equal(t, "org", imp.opts.Org)
	require.Equal(t, "project", imp.opts.Project)
	require.Equal(t, []string{"refs/heads/*"}, imp.opts.Refs)
	require.Equal(t, "main", imp.opts.DefaultBranch)
	require.Equal(t, gitimport.SymlinksFile, imp.opts.Symlinks)
	require.Equal(t, "game", imp.opts.SourceKey)
	require.True(t, imp.opts.Update)
	require.True(t, imp.opts.ResetState)
	require.False(t, imp.opts.DryRun)

	require.Contains(t, out, "Import into org/project:")
	require.Contains(t, out, "3 to import, 1 already on the server")
	require.Contains(t, out, "warning: commit 0123456: octopus merge")
	require.Contains(t, out, "Importing 3 commits...")
	require.Contains(t, out, "Imported 3 commits into org/project (1 already there, 2.0 KiB uploaded)")
	require.Contains(t, out, "  branch main: created\n")
	require.Contains(t, out, "  tag v1: created (note)\n")
	require.Contains(t, out, "warning: branch feature diverged: the branch has commits")
}

func TestImportGitCmd_JSON(t *testing.T) {
	imp := &fakeGitImporter{sum: importSummary()}
	out, errOut, err := runImportCmd(t, imp, importRun{}, "../game", importURL, "--yes", "--json")
	require.NoError(t, err)
	require.Nil(t, imp.progress)
	require.Contains(t, errOut, "Import into org/project:")
	var got importJSON
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, 3, got.Imported)
	require.Len(t, got.Refs, 3)
	require.Len(t, got.RefWarnings, 1)
	require.Equal(t, "diverged", got.RefWarnings[0].Status)
	require.Equal(t, "octopus", got.Warnings[0].Kind)
	require.Equal(t, 2, got.Report.Blobs)
	require.NotNil(t, got.Report.InvalidPaths)
}

func TestImportGitCmd_Confirmation(t *testing.T) {
	imp := &fakeGitImporter{sum: importSummary()}
	_, _, err := runImportCmd(t, imp, importRun{}, "../game", importURL)
	require.ErrorContains(t, err, "pass --yes")

	out, _, err := runImportCmd(t, imp, importRun{terminal: true, stdin: "y\n"}, "../game", importURL)
	require.NoError(t, err)
	require.Contains(t, out, "Import? [y/N]")

	_, _, err = runImportCmd(t, imp, importRun{terminal: true, stdin: "n\n"}, "../game", importURL)
	require.ErrorContains(t, err, "cancelled")
}

func TestImportGitCmd_DryRun(t *testing.T) {
	sum := importSummary()
	sum.DryRun = true
	sum.Refs = nil
	imp := &fakeGitImporter{sum: sum}
	out, _, err := runImportCmd(t, imp, importRun{}, "../game", importURL, "--dry-run")
	require.NoError(t, err)
	require.True(t, imp.opts.DryRun)
	require.Contains(t, out, "Dry run: nothing was imported.")
	require.Contains(t, out, "4 files written, 1 removed")

	sum.Report.InvalidPaths = []gitimport.PathIssue{{Commit: "abcdef0123", Path: `a\b`}}
	sum.Report.Symlinks = []gitimport.PathIssue{{Commit: "abcdef0123", Path: "link"}}
	sum.Report.Submodules = []gitimport.Submodule{{Commit: "abcdef0123", Path: "lib", URL: "https://x"}}
	out, _, err = runImportCmd(t, imp, importRun{}, "../game", importURL, "--dry-run")
	require.Equal(t, 1, ExitCode(err))
	require.Contains(t, out, `invalid path: a\b (commit abcdef0)`)

	out, _, err = runImportCmd(t, imp, importRun{}, "../game", importURL, "--dry-run", "--json")
	require.Equal(t, 1, ExitCode(err))
	var got importJSON
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.True(t, got.DryRun)
	require.Len(t, got.Report.InvalidPaths, 1)
	require.Equal(t, "https://x", got.Report.Submodules[0].URL)
}

func TestImportGitCmd_InvalidRefFails(t *testing.T) {
	sum := importSummary()
	sum.Refs = append(sum.Refs, clientgitimport.RefResult{Kind: "branch", Name: "bad", Status: clientgitimport.RefInvalid, Message: "invalid branch name"})
	out, _, err := runImportCmd(t, &fakeGitImporter{sum: sum}, importRun{}, "../game", importURL, "-y")
	require.Equal(t, 1, ExitCode(err))
	require.Contains(t, out, "error: branch bad: invalid branch name")
}

func TestImportGitCmd_Errors(t *testing.T) {
	imp := &fakeGitImporter{sum: importSummary()}
	cases := map[string][]string{
		"one window":   {"--depth", "3", "--snapshot"},
		"depth":        {"--depth", "-2"},
		"since":        {"--since", "yesterday"},
		"author map":   {"--author-map", filepath.Join(t.TempDir(), "missing")},
		"invalid url":  {},
		"failing run":  {},
		"connect fail": {},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			url := importURL
			run := importRun{}
			imp := imp
			switch name {
			case "invalid url":
				url = "http://example.com/only"
			case "failing run":
				imp = &fakeGitImporter{err: errors.New("server refused")}
			case "connect fail":
				run.conn = &fakeConnector{err: errors.New("dial failed")}
			}
			_, _, err := runImportCmd(t, imp, run, append([]string{"../game", url, "-y"}, extra...)...)
			require.Error(t, err)
		})
	}
}

func TestImportGitCmd_AuthorMapAndSince(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authors")
	require.NoError(t, os.WriteFile(path, []byte("Ann <ann@example.com> = 1a\n"), 0o600))
	imp := &fakeGitImporter{sum: importSummary()}
	_, _, err := runImportCmd(t, imp, importRun{}, "../game", importURL, "-y", "--author-map", path, "--since", "2020-01-02")
	require.NoError(t, err)
	require.Equal(t, 1, imp.opts.AuthorMap.Len())
	require.Equal(t, time.Date(2020, 1, 2, 0, 0, 0, 0, time.Local), imp.opts.Since)

	_, _, err = runImportCmd(t, imp, importRun{}, "../game", importURL, "-y", "--depth", "5")
	require.NoError(t, err)
	require.Equal(t, 5, imp.opts.Depth)
}

func TestParseSince(t *testing.T) {
	got, err := parseSince("2021-05-06T07:08:09Z")
	require.NoError(t, err)
	require.True(t, got.Equal(time.Date(2021, 5, 6, 7, 8, 9, 0, time.UTC)))
	_, err = parseSince("2021-05-06 07:08:09")
	require.NoError(t, err)
	_, err = parseSince("nope")
	require.ErrorContains(t, err, "invalid --since")
}

func TestDescribeWarning(t *testing.T) {
	require.Equal(t, "plain", describeWarning(gitimport.Warning{Message: "plain"}))
	require.Equal(t, "refs/heads/x commit abcdef0 a.txt: m", describeWarning(gitimport.Warning{Ref: "refs/heads/x", Commit: "abcdef0123", Path: "a.txt", Message: "m"}))
	require.Equal(t, "abc", shortSHA("abc"))
	require.Equal(t, "none", orNone(""))
}

func TestImportProgress(t *testing.T) {
	var buf bytes.Buffer
	p := &importProgress{r: newProgressRenderer(&buf)}
	p.ImportStart(0)
	p.ImportProgress(0, 0)
	p.ImportEnd()
	require.NotContains(t, buf.String(), "Importing")
	buf.Reset()
	p = &importProgress{r: newProgressRenderer(&buf)}
	p.ImportStart(4)
	p.ImportProgress(2, 1024)
	p.ImportEnd()
	require.Contains(t, buf.String(), "Importing 4 commits...")
	require.Contains(t, buf.String(), "2/4 commits, 1.0 KiB uploaded (50%)")
	require.Contains(t, buf.String(), "Imported 2 commits (1.0 KiB uploaded)")
}

func TestImportCmdRegistered(t *testing.T) {
	cmd := NewCli(&fakeUsecaseContainer{}, &fakeConnector{}).setupImportCmd()
	sub, _, err := cmd.Find([]string{"git"})
	require.NoError(t, err)
	require.Equal(t, "git", sub.Name())
}
