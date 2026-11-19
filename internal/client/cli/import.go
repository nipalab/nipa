package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/domain"
	clientgitimport "github.com/nipalab/nipa/internal/client/gitimport"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/gitimport"
)

type GitImporter interface {
	Run(ctx context.Context, opts clientgitimport.Options, progress clientgitimport.Progress) (*clientgitimport.Summary, error)
}

func (c *Cli) setupImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import history from another version control system",
	}
	cmd.AddCommand(c.setupImportGitCmd())
	return cmd
}

func (c *Cli) setupImportGitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git <git-path> <nipa-url>",
		Short: "Import a Git repository's history, branches and tags into a project",
		Long: "Import the commits, branches and tags of a local Git repository into an existing Nipa project. " +
			"Authors and dates are kept, and each Git commit is imported once: a rerun resumes an interrupted " +
			"import, and --update imports new Git commits and fast-forwards the branches. A branch that moved in " +
			"Nipa (diverged) or is protected is reported and left alone. Requires project admin.",
		Args:          cobra.ExactArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE:          c.runImportGit,
	}
	f := cmd.Flags()
	f.StringSlice("refs", nil, "Git ref patterns to import (comma separated; default refs/heads/*,refs/tags/*)")
	f.String("default-branch", "", "Default branch of the project (default: the Git HEAD branch)")
	f.Int("depth", 0, "Import only the last N commits of each ref")
	f.String("since", "", "Import only commits committed on or after this date (YYYY-MM-DD or RFC 3339)")
	f.Bool("snapshot", false, "Import one commit per ref tip, without history")
	f.String("symlinks", string(gitimport.SymlinksSkip), "How to import symlinks: skip, or file (store the target path)")
	f.String("author-map", "", "Mailmap-style file mapping Git authors to Nipa users: Name <email> = user-id")
	f.String("source-key", gitimport.DefaultSourceKey, "Name of the imported source, kept in every commit's origin")
	f.Bool("update", false, "Fast-forward branches that already exist in the project")
	f.Bool("reset-state", false, "Forget the local import state (which content was already uploaded)")
	f.Bool("dry-run", false, "Report what would be imported without writing anything")
	f.BoolP("yes", "y", false, "Import without asking for confirmation")
	addJSONFlag(cmd)
	return cmd
}

func (c *Cli) runImportGit(cmd *cobra.Command, args []string) error {
	opts, err := importOptions(cmd, args[0])
	if err != nil {
		return err
	}
	nipaURL, err := domain.ParseNipaUrl(args[1])
	if err != nil {
		return err
	}
	opts.Host, opts.Org, opts.Project = nipaURL.Host, nipaURL.Org, nipaURL.Project

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	if err := c.connector.Connect(ctx, nipaURL.Origin); err != nil {
		return err
	}
	if err := c.useCase.Auth().MakeSureLoggedIn(ctx, nipaURL.Host); err != nil {
		return err
	}

	asJSON := jsonRequested(cmd)
	out := cmd.OutOrStdout()
	info := out
	if asJSON {
		info = cmd.ErrOrStderr()
	}
	opts.Confirm = func(sum *clientgitimport.Summary) error {
		writeImportPreview(info, sum)
		return c.confirmImport(cmd, info)
	}
	var progress clientgitimport.Progress
	if !asJSON {
		progress = &importProgress{r: newProgressRenderer(out)}
	}

	sum, err := c.useCase.GitImport().Run(ctx, opts, progress)
	if err != nil {
		return err
	}
	if asJSON {
		if err := output.WriteJSON(out, importJSONOf(sum)); err != nil {
			return err
		}
	} else {
		writeImportSummary(out, sum)
	}
	if sum.DryRun && len(sum.Report.InvalidPaths) > 0 {
		return &ExitError{Code: 1, Err: fmt.Errorf("the git history has %d path(s) Nipa cannot store", len(sum.Report.InvalidPaths))}
	}
	if bad := sum.RefErrors(); len(bad) > 0 {
		return &ExitError{Code: 1, Err: fmt.Errorf("%d ref(s) could not be set", len(bad))}
	}
	return nil
}

func importOptions(cmd *cobra.Command, gitPath string) (clientgitimport.Options, error) {
	f := cmd.Flags()
	opts := clientgitimport.Options{GitPath: gitPath}
	opts.Refs, _ = f.GetStringSlice("refs")
	opts.DefaultBranch, _ = f.GetString("default-branch")
	opts.Depth, _ = f.GetInt("depth")
	opts.Snapshot, _ = f.GetBool("snapshot")
	symlinks, _ := f.GetString("symlinks")
	opts.Symlinks = gitimport.SymlinkPolicy(symlinks)
	opts.SourceKey, _ = f.GetString("source-key")
	opts.Update, _ = f.GetBool("update")
	opts.ResetState, _ = f.GetBool("reset-state")
	opts.DryRun, _ = f.GetBool("dry-run")
	since, _ := f.GetString("since")

	windows := 0
	for _, set := range []bool{opts.Depth != 0, since != "", opts.Snapshot} {
		if set {
			windows++
		}
	}
	if windows > 1 {
		return opts, domain.NewUserError("use only one of --depth, --since and --snapshot")
	}
	if opts.Depth < 0 {
		return opts, domain.NewUserError("--depth must be positive")
	}
	if since != "" {
		t, err := parseSince(since)
		if err != nil {
			return opts, err
		}
		opts.Since = t
	}
	if path, _ := f.GetString("author-map"); path != "" {
		m, err := clientgitimport.LoadAuthorMap(path)
		if err != nil {
			return opts, domain.NewUserError(err.Error())
		}
		opts.AuthorMap = m
	}
	return opts, nil
}

func parseSince(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, time.DateTime, time.DateOnly} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, domain.NewUserError(fmt.Sprintf("invalid --since %q: want YYYY-MM-DD or RFC 3339", s))
}

func (c *Cli) confirmImport(cmd *cobra.Command, out io.Writer) error {
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	if !c.interactive(cmd.InOrStdin()) {
		return domain.NewUserError("refusing to import without confirmation; pass --yes or run in a terminal")
	}
	ok, err := confirm(cmd.InOrStdin(), out, "Import? [y/N] ")
	if err != nil {
		return err
	}
	if !ok {
		return domain.NewUserError("cancelled; nothing was imported")
	}
	return nil
}

func writeImportPreview(w io.Writer, sum *clientgitimport.Summary) {
	r := sum.Report
	_, _ = fmt.Fprintf(w, "Import into %s:\n", sum.Target)
	_, _ = fmt.Fprintf(w, "  commits:  %d to import, %d already on the server\n", sum.Commits, sum.Known)
	_, _ = fmt.Fprintf(w, "  refs:     %d branches, %d tags (default branch %s)\n", sum.Branches, sum.Tags, orNone(sum.DefaultBranch))
	if r != nil {
		_, _ = fmt.Fprintf(w, "  content:  %d distinct blobs, %s\n", r.Blobs, humanBytes(r.Bytes))
		_, _ = fmt.Fprintf(w, "  changes:  %d files written, %d removed, %d empty commits\n", r.Files, r.Removed, len(r.EmptyCommits))
	}
	for _, warning := range sum.Warnings {
		_, _ = fmt.Fprintf(w, "warning: %s\n", describeWarning(warning))
	}
}

func writeImportSummary(w io.Writer, sum *clientgitimport.Summary) {
	if sum.DryRun {
		writeImportPreview(w, sum)
		for _, p := range sum.Report.InvalidPaths {
			_, _ = fmt.Fprintf(w, "invalid path: %s (commit %s)\n", p.Path, shortSHA(p.Commit))
		}
		_, _ = fmt.Fprintln(w, "Dry run: nothing was imported.")
		return
	}
	_, _ = fmt.Fprintf(w, "Imported %d commits into %s (%d already there, %s uploaded)\n", sum.Imported, sum.Target, sum.Known, humanBytes(sum.UploadedBytes))
	for _, r := range sum.Refs {
		line := fmt.Sprintf("  %s %s: %s", r.Kind, r.Name, r.Status)
		if r.Message != "" && !r.Warning() && r.Status != clientgitimport.RefInvalid {
			line += " (" + r.Message + ")"
		}
		_, _ = fmt.Fprintln(w, line)
	}
	for _, r := range sum.RefWarnings() {
		_, _ = fmt.Fprintf(w, "warning: %s %s %s: %s\n", r.Kind, r.Name, r.Status, r.Message)
	}
	for _, r := range sum.RefErrors() {
		_, _ = fmt.Fprintf(w, "error: %s %s: %s\n", r.Kind, r.Name, r.Message)
	}
}

func describeWarning(w gitimport.Warning) string {
	var where []string
	if w.Ref != "" {
		where = append(where, w.Ref)
	}
	if w.Commit != "" {
		where = append(where, "commit "+shortSHA(w.Commit))
	}
	if w.Path != "" {
		where = append(where, w.Path)
	}
	if len(where) == 0 {
		return w.Message
	}
	return strings.Join(where, " ") + ": " + w.Message
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

type importProgress struct {
	r     *progressRenderer
	total int
	done  int
	bytes int64
}

func (p *importProgress) ImportStart(commits int) {
	p.total = commits
	if commits > 0 {
		_, _ = fmt.Fprintf(p.r.w, "Importing %d commits...\n", commits)
	}
}

func (p *importProgress) ImportProgress(commits int, bytes int64) {
	p.done, p.bytes = commits, bytes
	pct := 100
	if p.total > 0 {
		pct = commits * 100 / p.total
	}
	p.r.writeLine(fmt.Sprintf("  %d/%d commits, %s uploaded (%d%%)", commits, p.total, humanBytes(bytes), pct))
}

func (p *importProgress) ImportEnd() {
	if p.total == 0 {
		return
	}
	p.r.writeLine(fmt.Sprintf("  Imported %d commits (%s uploaded)", p.done, humanBytes(p.bytes)))
	_, _ = fmt.Fprint(p.r.w, "\n")
	p.r.lineLen = 0
}

type importRefJSON struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	CommitID string `json:"commit_id,omitempty"`
	Message  string `json:"message,omitempty"`
}

type importWarningJSON struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type importPathJSON struct {
	Commit string `json:"commit"`
	Path   string `json:"path"`
}

type importSubmoduleJSON struct {
	Commit string `json:"commit"`
	Path   string `json:"path"`
	URL    string `json:"url,omitempty"`
}

type importReportJSON struct {
	Commits      int                   `json:"commits"`
	Branches     int                   `json:"branches"`
	Tags         int                   `json:"tags"`
	Blobs        int                   `json:"blobs"`
	Bytes        int64                 `json:"bytes"`
	Files        int                   `json:"files"`
	Removed      int                   `json:"removed"`
	EmptyCommits []string              `json:"empty_commits"`
	Octopus      []string              `json:"octopus"`
	InvalidPaths []importPathJSON      `json:"invalid_paths"`
	Symlinks     []importPathJSON      `json:"symlinks"`
	Submodules   []importSubmoduleJSON `json:"submodules"`
}

type importJSON struct {
	Target        string              `json:"target"`
	DryRun        bool                `json:"dry_run"`
	Commits       int                 `json:"commits"`
	Known         int                 `json:"known"`
	Imported      int                 `json:"imported"`
	UploadedBytes int64               `json:"uploaded_bytes"`
	BlobsRead     int64               `json:"blobs_read"`
	BlobHits      int64               `json:"blob_hits"`
	Branches      int                 `json:"branches"`
	Tags          int                 `json:"tags"`
	DefaultBranch string              `json:"default_branch"`
	Refs          []importRefJSON     `json:"refs"`
	RefWarnings   []importRefJSON     `json:"ref_warnings"`
	Warnings      []importWarningJSON `json:"warnings"`
	Report        *importReportJSON   `json:"report,omitempty"`
}

func importJSONOf(sum *clientgitimport.Summary) importJSON {
	out := importJSON{
		Target:        sum.Target,
		DryRun:        sum.DryRun,
		Commits:       sum.Commits,
		Known:         sum.Known,
		Imported:      sum.Imported,
		UploadedBytes: sum.UploadedBytes,
		BlobsRead:     sum.BlobsRead,
		BlobHits:      sum.BlobHits,
		Branches:      sum.Branches,
		Tags:          sum.Tags,
		DefaultBranch: sum.DefaultBranch,
		Refs:          refsJSON(sum.Refs),
		RefWarnings:   refsJSON(sum.RefWarnings()),
		Warnings:      []importWarningJSON{},
	}
	for _, w := range sum.Warnings {
		out.Warnings = append(out.Warnings, importWarningJSON{Kind: string(w.Kind), Ref: w.Ref, Commit: w.Commit, Path: w.Path, Message: w.Message})
	}
	if r := sum.Report; r != nil {
		rep := &importReportJSON{
			Commits: r.Commits, Branches: r.Branches, Tags: r.Tags, Blobs: r.Blobs, Bytes: r.Bytes,
			Files: r.Files, Removed: r.Removed,
			EmptyCommits: append([]string{}, r.EmptyCommits...),
			Octopus:      append([]string{}, r.Octopus...),
			InvalidPaths: pathsJSON(r.InvalidPaths),
			Symlinks:     pathsJSON(r.Symlinks),
			Submodules:   []importSubmoduleJSON{},
		}
		for _, s := range r.Submodules {
			rep.Submodules = append(rep.Submodules, importSubmoduleJSON{Commit: s.Commit, Path: s.Path, URL: s.URL})
		}
		out.Report = rep
	}
	return out
}

func refsJSON(refs []clientgitimport.RefResult) []importRefJSON {
	out := make([]importRefJSON, 0, len(refs))
	for _, r := range refs {
		out = append(out, importRefJSON{Kind: r.Kind, Name: r.Name, Status: r.Status, CommitID: r.CommitID, Message: r.Message})
	}
	return out
}

func pathsJSON(paths []gitimport.PathIssue) []importPathJSON {
	out := make([]importPathJSON, 0, len(paths))
	for _, p := range paths {
		out = append(out, importPathJSON{Commit: p.Commit, Path: p.Path})
	}
	return out
}
