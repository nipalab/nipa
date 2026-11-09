package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nipalab/nipa/internal/assetdiff"
	"github.com/nipalab/nipa/internal/assetrefs"
	"github.com/nipalab/nipa/internal/client/aicontext"
	"github.com/nipalab/nipa/internal/client/assetscan"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/godotmeta"
	"github.com/nipalab/nipa/internal/unitymeta"
)

const (
	defaultAssetRows    = 200
	maxAssetRows        = 2000
	defaultAssetChanges = 500
	maxAssetChanges     = 5000
	defaultSummaryFiles = 50
	maxSummaryFiles     = 200

	maxGUIDChecks = 256
	maxMetaBytes  = 1 << 20

	engineUnity = "unity"
	engineGodot = "godot"

	sideWorking  = assetscan.SideWorking
	sideSnapshot = assetscan.SideSnapshot
)

var (
	errMetaBudget   = errors.New("too many sidecar files to compare")
	errMetaTooLarge = errors.New("sidecar file too large")

	godotMergeExts = []string{".tscn", ".tres"}
)

func addAssetTools(srv *mcp.Server, s *server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_asset_diff",
		Description: "Compare two versions of one asset structurally: Unity/Godot scene objects and properties, image size/format/pixel changes, 3D model stats, audio loudness and duration, CSV/TSV cells or text lines. Sides are working, snapshot (last synced version) or a revision (branch, tag, base36 commit id, HEAD). include_images attaches old/new PNG thumbnails as image content.",
		Annotations: readOnly(),
	}, s.assetDiff)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_describe_changes",
		Description: "Summarize every changed file in one headline each (scene objects changed, image resized, triangles added, loudness moved...) for the working copy or revisions, with the same arguments as nipa_diff. At most max_files files (default 50, at most 200); the rest are counted in omitted.",
		Annotations: readOnly(),
	}, s.describeChanges)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_asset_check",
		Description: "Check changed files against the repository's .nipaassets.yml asset budgets (texture size, power of two, triangles, vertices, audio length and peak, file size, naming), with the same arguments as nipa_describe_changes. Only the new side of each change is checked. config is false when the working copy has no .nipaassets.yml.",
		Annotations: readOnly(),
	}, s.assetCheck)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_mr_describe_changes",
		Description: "Summarize the files a merge request changes, one asset-aware headline per file, comparing the merge base (or the target head when the merge base is unknown, see base.kind) with the source head.",
		Annotations: readOnly(),
	}, s.mrDescribeChanges)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_meta_warnings",
		Description: "Lint pending changes of Unity (.meta) or Godot (.import, .uid) projects for sidecar mistakes: missing, orphaned, not staged or changed GUID/UID. The engine is detected from project.godot or ProjectSettings/ProjectVersion.txt when omitted. Runs offline.",
		Annotations: readOnly(),
	}, s.metaWarnings)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_asset_refs",
		Description: "List what one Unity (Force Text) or Godot text asset references by GUID, uid:// or res:// path (uses) and which scenes, prefabs or resources reference it (used_by), from the working copy. Ask before deleting, moving or regenerating the .meta of an asset. References into paths a sparse clone does not hold are listed as unknown. At most max_files source files are parsed (default 5000, at most 20000); truncated is set beyond. Runs offline.",
		Annotations: readOnly(),
	}, s.assetRefs)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_asset_impact",
		Description: "Check pending changes (working copy against the last synced snapshot) for broken Unity/Godot asset references: removed or renamed assets, changed GUID/UID or new references that do not resolve (references that were already unresolved are not reported), plus assets left unused (scenes, scripts, Resources/StreamingAssets/Editor/Plugins/addons excluded; omitted when the scan is truncated). Unstaged working changes are part of the new side. Warn-only, runs offline.",
		Annotations: readOnly(),
	}, s.assetImpact)
	if s.allowed(CapMerge) {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_godot_merge",
			Description: "Structurally three-way merge one conflicted Godot text scene or resource (.tscn, .tres) of the pending merge or revert. A clean merge is written to the working file and staged as resolved; otherwise nothing is written and the conflicts are listed for nipa_resolve_conflict.",
			Annotations: destructive(),
		}, s.godotMerge)
	}
}

func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return strings.TrimPrefix(path.Clean("/"+p), "/")
}

func (s *server) openDiff(repo string) (string, UseCases, error) {
	root, uc, err := s.open(repo)
	if err != nil {
		return "", UseCases{}, toolError(err)
	}
	if uc.Diff == nil {
		uc.close()
		return "", UseCases{}, notConfigured("diff is")
	}
	return root, uc, nil
}

type assetDiffInput struct {
	Repo          string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Path          string `json:"path" jsonschema:"Repo-relative path of the asset (the new path for a rename)"`
	OldPath       string `json:"old_path,omitempty" jsonschema:"Old repo-relative path when the asset was renamed"`
	Old           string `json:"old,omitempty" jsonschema:"Old side: working, snapshot (default) or a revision (branch, tag, base36 commit id, HEAD)"`
	New           string `json:"new,omitempty" jsonschema:"New side: working (default), snapshot or a revision"`
	IncludeImages bool   `json:"include_images,omitempty" jsonschema:"Attach old/new image thumbnails as PNG image content"`
	MaxRows       int    `json:"max_rows,omitempty" jsonschema:"Maximum changed table rows (default 200, at most 2000)"`
	MaxChanges    int    `json:"max_changes,omitempty" jsonschema:"Maximum scene object changes (default 500, at most 5000)"`
}

func (s *server) assetDiff(ctx context.Context, _ *mcp.CallToolRequest, in assetDiffInput) (*mcp.CallToolResult, *assetdiff.Result, error) {
	p := cleanPath(in.Path)
	if p == "" {
		return nil, nil, toolError(domain.NewUserError("a path is required"))
	}
	oldPath := cleanPath(in.OldPath)
	if oldPath == "" {
		oldPath = p
	}
	oldSide, newSide := strings.TrimSpace(in.Old), strings.TrimSpace(in.New)
	if oldSide == "" {
		oldSide = sideSnapshot
	}
	if newSide == "" {
		newSide = sideWorking
	}
	root, uc, err := s.openDiff(in.Repo)
	if err != nil {
		return nil, nil, err
	}
	defer uc.close()
	res, err := assetdiff.Compare(ctx, assetscan.BlobLoader(uc.Diff, root), assetscan.SideRef(oldSide, oldPath), assetscan.SideRef(newSide, p), assetdiff.Options{
		Images:     in.IncludeImages,
		MaxRows:    clampLimit(in.MaxRows, defaultAssetRows, maxAssetRows),
		MaxChanges: clampLimit(in.MaxChanges, defaultAssetChanges, maxAssetChanges),
		GUIDs:      assetdiff.WorkingGUIDs(root),
	})
	if errors.Is(err, assetdiff.ErrMissing) {
		return nil, nil, toolError(domain.NewUserError(fmt.Sprintf("%s does not exist in %s or %s", p, oldSide, newSide)))
	}
	if err != nil {
		return nil, nil, toolError(err)
	}
	if !in.IncludeImages || res.Image == nil {
		return nil, res, nil
	}
	return imageResult(res)
}

// imageResult moves the thumbnails out of the structured output into image
// content blocks so the base64 bytes are not sent twice.
func imageResult(res *assetdiff.Result) (*mcp.CallToolResult, *assetdiff.Result, error) {
	var images []mcp.Content
	for _, side := range []struct {
		label string
		img   *assetdiff.ImageSide
	}{{"old", res.Image.Old}, {"new", res.Image.New}} {
		if side.img == nil || len(side.img.ThumbnailPNG) == 0 {
			continue
		}
		images = append(images,
			&mcp.TextContent{Text: fmt.Sprintf("%s thumbnail of %s (%dx%d source)", side.label, res.Path, side.img.Width, side.img.Height)},
			&mcp.ImageContent{Data: side.img.ThumbnailPNG, MIMEType: "image/png"})
		side.img.ThumbnailPNG = nil
	}
	if len(images) == 0 {
		return nil, res, nil
	}
	data, err := json.Marshal(res)
	if err != nil {
		return nil, nil, err
	}
	content := append([]mcp.Content{&mcp.TextContent{Text: string(data)}}, images...)
	return &mcp.CallToolResult{Content: content}, res, nil
}

type describeInput struct {
	Repo      string   `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Revisions []string `json:"revisions,omitempty" jsonschema:"Up to two revisions: branch name, tag, base36 commit id or HEAD/@; omit to compare the working tree against the last synced snapshot"`
	Paths     []string `json:"paths,omitempty" jsonschema:"Limit to these repo-relative file or directory paths"`
	Staged    bool     `json:"staged,omitempty" jsonschema:"Only describe changes staged for the next push"`
	MergeBase bool     `json:"merge_base,omitempty" jsonschema:"Compare the merge base of two revisions against the second (three-dot diff)"`
	NoCache   bool     `json:"no_cache,omitempty" jsonschema:"Read every working file instead of trusting the stat cache"`
	MaxFiles  int      `json:"max_files,omitempty" jsonschema:"Maximum number of files described (default 50, at most 200)"`
}

func (in describeInput) request() assetscan.Request {
	return assetscan.Request{Revisions: in.Revisions, Paths: in.Paths, Staged: in.Staged, MergeBase: in.MergeBase, NoCache: in.NoCache}
}

type describeOutput struct {
	Old     string              `json:"old"`
	New     string              `json:"new"`
	Files   []assetdiff.Summary `json:"files"`
	Omitted int                 `json:"omitted"`
}

func (s *server) describeChanges(ctx context.Context, _ *mcp.CallToolRequest, in describeInput) (*mcp.CallToolResult, describeOutput, error) {
	var zero describeOutput
	if err := validateRevisions(in.Revisions, in.Staged, in.MergeBase); err != nil {
		return nil, zero, err
	}
	root, uc, err := s.openDiff(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	changes, err := assetscan.Enumerate(ctx, uc.Diff, root, in.request())
	if err != nil {
		return nil, zero, toolError(err)
	}
	oldSide, newSide := changes.Old, changes.New
	items := make([]assetdiff.Item, 0, len(changes.Files))
	for _, c := range changes.Files {
		items = append(items, c.Item)
	}
	out, err := s.summarize(ctx, uc, root, items, in.MaxFiles)
	if err != nil {
		return nil, zero, err
	}
	return nil, describeOutput{Old: oldSide, New: newSide, Files: out.Files, Omitted: out.Omitted}, nil
}

type assetCheckInput struct {
	Repo      string   `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Revisions []string `json:"revisions,omitempty" jsonschema:"Up to two revisions: branch name, tag, base36 commit id or HEAD/@; omit to check the working tree against the last synced snapshot"`
	Paths     []string `json:"paths,omitempty" jsonschema:"Limit to these repo-relative file or directory paths"`
	Staged    bool     `json:"staged,omitempty" jsonschema:"Only check changes staged for the next push"`
	MergeBase bool     `json:"merge_base,omitempty" jsonschema:"Compare the merge base of two revisions against the second (three-dot diff)"`
	NoCache   bool     `json:"no_cache,omitempty" jsonschema:"Read every working file instead of trusting the stat cache"`
}

func (s *server) assetCheck(ctx context.Context, _ *mcp.CallToolRequest, in assetCheckInput) (*mcp.CallToolResult, *assetscan.Report, error) {
	if err := validateRevisions(in.Revisions, in.Staged, in.MergeBase); err != nil {
		return nil, nil, err
	}
	root, uc, err := s.openDiff(in.Repo)
	if err != nil {
		return nil, nil, err
	}
	defer uc.close()
	report, err := assetscan.Check(ctx, uc.Diff, root, assetscan.Request{
		Revisions: in.Revisions, Paths: in.Paths, Staged: in.Staged, MergeBase: in.MergeBase, NoCache: in.NoCache,
	})
	if err != nil {
		return nil, nil, toolError(err)
	}
	return nil, report, nil
}

func (s *server) summarize(ctx context.Context, uc UseCases, root string, items []assetdiff.Item, maxFiles int) (*assetdiff.Summaries, error) {
	out, err := assetdiff.Summarize(ctx, assetscan.BlobLoader(uc.Diff, root), items, assetdiff.Options{
		MaxFiles: clampLimit(maxFiles, defaultSummaryFiles, maxSummaryFiles),
		GUIDs:    assetdiff.WorkingGUIDs(root),
	})
	if err != nil {
		return nil, toolError(err)
	}
	return out, nil
}

type mrDescribeInput struct {
	Repo     string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Number   int64  `json:"number" jsonschema:"Merge request number"`
	MaxFiles int    `json:"max_files,omitempty" jsonschema:"Maximum number of files described (default 50, at most 200)"`
}

type mrSide = aicontext.Side

type mrDescribeOutput struct {
	Number  int64               `json:"number"`
	Base    mrSide              `json:"base"`
	Head    mrSide              `json:"head"`
	Files   []assetdiff.Summary `json:"files"`
	Omitted int                 `json:"omitted"`
}

func (s *server) mrDescribeChanges(ctx context.Context, _ *mcp.CallToolRequest, in mrDescribeInput) (*mcp.CallToolResult, mrDescribeOutput, error) {
	var zero mrDescribeOutput
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	if uc.Diff == nil {
		return nil, zero, notConfigured("diff is")
	}
	mr, mergeability, err := uc.MR.View(ctx, root, mrRef(in.Number))
	if err != nil {
		return nil, zero, toolError(err)
	}
	files, err := uc.MR.Diff(ctx, root, mrRef(in.Number))
	if err != nil {
		return nil, zero, toolError(err)
	}
	base, head := mrSides(mr, mergeability)
	items := make([]assetdiff.Item, 0, len(files))
	for _, f := range files {
		if f != nil {
			items = append(items, assetscan.ChangeItem(f.Path, f.OldPath, f.Status, base.Revision, head.Revision))
		}
	}
	out, err := s.summarize(ctx, uc, root, items, in.MaxFiles)
	if err != nil {
		return nil, zero, err
	}
	return nil, mrDescribeOutput{Number: in.Number, Base: base, Head: head, Files: out.Files, Omitted: out.Omitted}, nil
}

func mrSides(mr *domain.MergeRequest, m *domain.Mergeability) (mrSide, mrSide) {
	return aicontext.MRSides(mr, m)
}

type metaWarningsInput struct {
	Repo   string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Engine string `json:"engine,omitempty" jsonschema:"unity, godot, or empty to detect from the clone root"`
}

type metaWarning struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Related string `json:"related,omitempty"`
	Message string `json:"message"`
}

type metaWarningsOutput struct {
	Engine   string        `json:"engine,omitempty"`
	Warnings []metaWarning `json:"warnings"`
}

func (s *server) metaWarnings(ctx context.Context, _ *mcp.CallToolRequest, in metaWarningsInput) (*mcp.CallToolResult, metaWarningsOutput, error) {
	var zero metaWarningsOutput
	engine := strings.ToLower(strings.TrimSpace(in.Engine))
	if engine != "" && engine != engineUnity && engine != engineGodot {
		return nil, zero, toolError(domain.NewUserError(fmt.Sprintf("unknown engine %q (expected unity or godot)", in.Engine)))
	}
	wc, cleanup, err := s.workingCopy(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer cleanup()
	root, uc, err := s.openDiff(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	st, err := wc.Status(ctx)
	if err != nil {
		return nil, zero, toolError(err)
	}
	if engine == "" {
		engine = detectEngine(root)
	}
	out := metaWarningsOutput{Engine: engine, Warnings: []metaWarning{}}
	changes, unity := unitymeta.ChangesFromStatus(unitymeta.StatusPaths{
		Staged: st.Staged, Added: st.Added, Deleted: st.Deleted,
		Modified: st.Modified, Missing: st.Missing, Untracked: st.Untracked,
	})
	checks := 0
	oldSidecar := func(p string) ([]byte, error) {
		if checks++; checks > maxGUIDChecks {
			return nil, errMetaBudget
		}
		return readSnapshotSmall(ctx, uc.Diff, root, p)
	}
	fsys := os.DirFS(root)
	if engine != engineGodot && unity {
		for _, w := range unitymeta.Lint(fsys, changes, oldSidecar) {
			out.Warnings = append(out.Warnings, metaWarning{Path: w.Path, Kind: string(w.Kind), Related: w.Related, Message: w.Message})
		}
	}
	if engine != engineUnity {
		godot := make([]godotmeta.Change, len(changes))
		for i, c := range changes {
			godot[i] = godotmeta.Change{Path: c.Path, Status: c.Status, Staged: c.Staged}
		}
		for _, w := range godotmeta.Lint(fsys, godot, oldSidecar) {
			out.Warnings = append(out.Warnings, metaWarning{Path: w.Path, Kind: string(w.Kind), Related: w.Related, Message: w.Message})
		}
	}
	return nil, out, nil
}

func detectEngine(root string) string {
	if info, err := os.Stat(filepath.Join(root, "project.godot")); err == nil && info.Mode().IsRegular() {
		return engineGodot
	}
	if info, err := os.Stat(filepath.Join(root, "ProjectSettings", "ProjectVersion.txt")); err == nil && info.Mode().IsRegular() {
		return engineUnity
	}
	return ""
}

func readSnapshotSmall(ctx context.Context, d *usecase.Diff, root, p string) ([]byte, error) {
	var data []byte
	err := d.ReadBlob(ctx, root, usecase.BlobRequest{Source: usecase.BlobSnapshot, Path: p}, func(e diff.Entry, r io.Reader) error {
		if e.SizeBytes > maxMetaBytes {
			return errMetaTooLarge
		}
		var err error
		data, err = io.ReadAll(io.LimitReader(r, maxMetaBytes+1))
		if err == nil && len(data) > maxMetaBytes {
			return errMetaTooLarge
		}
		return err
	})
	return data, err
}

type assetRefsInput struct {
	Repo     string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Path     string `json:"path" jsonschema:"Repo-relative path of the asset"`
	MaxFiles int    `json:"max_files,omitempty" jsonschema:"Maximum source files parsed (default 5000, at most 20000)"`
}

func (s *server) assetRefs(ctx context.Context, _ *mcp.CallToolRequest, in assetRefsInput) (*mcp.CallToolResult, *assetscan.RefsReport, error) {
	p := cleanPath(in.Path)
	if p == "" {
		return nil, nil, toolError(domain.NewUserError("a path is required"))
	}
	root, uc, err := s.openDiff(in.Repo)
	if err != nil {
		return nil, nil, err
	}
	defer uc.close()
	report, err := assetscan.Refs(ctx, uc.Diff, root, p, assetrefs.Options{MaxFiles: in.MaxFiles})
	if err != nil {
		return nil, nil, toolError(err)
	}
	return nil, report, nil
}

type assetImpactInput struct {
	Repo     string   `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Paths    []string `json:"paths,omitempty" jsonschema:"Limit the checked changes to these repo-relative file or directory paths"`
	Staged   bool     `json:"staged,omitempty" jsonschema:"Only check changes staged for the next push"`
	MaxFiles int      `json:"max_files,omitempty" jsonschema:"Maximum source files parsed (default 5000, at most 20000)"`
}

func (s *server) assetImpact(ctx context.Context, _ *mcp.CallToolRequest, in assetImpactInput) (*mcp.CallToolResult, *assetrefs.Impact, error) {
	root, uc, err := s.openDiff(in.Repo)
	if err != nil {
		return nil, nil, err
	}
	defer uc.close()
	changes, err := assetscan.Enumerate(ctx, uc.Diff, root, assetscan.Request{Paths: in.Paths, Staged: in.Staged})
	if err != nil {
		return nil, nil, toolError(err)
	}
	out, err := assetscan.Impact(ctx, uc.Diff, root, changes, assetrefs.Options{MaxFiles: in.MaxFiles})
	if err != nil {
		return nil, nil, toolError(err)
	}
	if out == nil {
		out = &assetrefs.Impact{Broken: []assetrefs.BrokenRef{}, NewlyUnused: []string{}}
	}
	return nil, out, nil
}

type godotMergeInput struct {
	Repo string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Path string `json:"path" jsonschema:"Repo-relative path of the conflicted .tscn or .tres file"`
}

type godotMergeOutput struct {
	Path      string                    `json:"path"`
	Clean     bool                      `json:"clean"`
	Conflicts []assetdiff.GodotConflict `json:"conflicts"`
	Status    *output.Status            `json:"status,omitempty"`
	Next      string                    `json:"next,omitempty"`
}

func (s *server) godotMerge(ctx context.Context, _ *mcp.CallToolRequest, in godotMergeInput) (*mcp.CallToolResult, godotMergeOutput, error) {
	var zero godotMergeOutput
	p := cleanPath(in.Path)
	if p == "" || !slices.Contains(godotMergeExts, strings.ToLower(path.Ext(p))) {
		return nil, zero, toolError(domain.NewUserError(fmt.Sprintf("%q is not a Godot text scene or resource (.tscn, .tres)", in.Path)))
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	wc, lr, root, cleanup, err := s.localWorkingCopy(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer cleanup()
	c := usecase.NewConflicts(lr, wc, root)
	set, err := c.List()
	if err != nil {
		return nil, zero, toolError(err)
	}
	if set == nil {
		return nil, zero, toolError(domain.NewUserError("no merge or revert is waiting"))
	}
	idx := slices.IndexFunc(set.Files, func(f domain.ConflictFile) bool { return f.Path == p })
	if idx < 0 {
		return nil, zero, toolError(domain.NewUserError(fmt.Sprintf("%s has no conflict to resolve", p)))
	}
	if set.Files[idx].Binary {
		return nil, zero, toolError(domain.NewUserError(fmt.Sprintf("%s is stored as binary; pick ours or theirs", p)))
	}
	_, uc, err := s.openDiff(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	var refs [3]*assetdiff.Ref
	for i, rev := range []string{set.Base.ID, set.Ours.ID, set.Theirs.ID} {
		if rev != "" {
			refs[i] = &assetdiff.Ref{Source: string(usecase.BlobRevision), Revision: rev, Path: p}
		}
	}
	m, err := assetdiff.MergeGodot(ctx, assetscan.BlobLoader(uc.Diff, root), refs[0], refs[1], refs[2])
	var mergeErr *assetdiff.MergeError
	switch {
	case errors.Is(err, assetdiff.ErrTooLarge):
		return nil, zero, toolError(domain.NewUserError(fmt.Sprintf("%s is too large to merge", p)))
	case errors.As(err, &mergeErr):
		return nil, zero, toolError(domain.NewUserError(fmt.Sprintf("%s cannot be merged: %v", p, mergeErr.Err)))
	case err != nil:
		return nil, zero, toolError(err)
	}
	out := godotMergeOutput{Path: p, Clean: m.Clean, Conflicts: m.Conflicts}
	if out.Conflicts == nil {
		out.Conflicts = []assetdiff.GodotConflict{}
	}
	if !m.Clean {
		out.Next = "settle the file with nipa_resolve_conflict (ours, theirs or content)"
		return nil, out, nil
	}
	if err := c.Resolve(ctx, p, usecase.Resolution{Content: bytes.NewReader(m.Merged)}); err != nil {
		return nil, zero, toolError(err)
	}
	st, err := wc.Status(ctx)
	if err != nil {
		return nil, zero, toolError(err)
	}
	status := output.NewStatus(st)
	out.Status = &status
	return nil, out, nil
}
