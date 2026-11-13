package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/assetdiff"
	"github.com/nipalab/nipa/internal/assetrefs"
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/assetscan"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	godotBase = `[gd_scene format=3]

[node name="Main" type="Node2D"]

[node name="Player" type="Node2D" parent="."]
speed = 5
`
	godotOurs = `[gd_scene format=3]

[node name="Main" type="Node2D"]

[node name="Player" type="Node2D" parent="."]
speed = 7
`
	godotTheirs = `[gd_scene format=3]

[node name="Main" type="Node2D"]
visible = false

[node name="Player" type="Node2D" parent="."]
speed = 5
`
	godotOther = `[gd_scene format=3]

[node name="Main" type="Node2D"]

[node name="Player" type="Node2D" parent="."]
speed = 9
`
)

func testPNG(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// fakeDiffClient serves commits made of flat root files, plus their chunks.
type fakeDiffClient struct {
	branches  map[string]string
	commits   map[string]map[string]string
	mergeBase string
	err       error
}

func (f *fakeDiffClient) Connect(context.Context, string) error { return f.err }

func (f *fakeDiffClient) GetBranchByName(_ context.Context, _, _, name string) (*serverDomain.Branch, error) {
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

func (f *fakeDiffClient) GetTagByName(context.Context, string, string, string) (*domain.Tag, error) {
	return nil, &domain.Error{Code: 404, Message: "tag not found"}
}

func (f *fakeDiffClient) GetCommit(_ context.Context, _, _, id string) (*domain.CommitDetail, error) {
	files, ok := f.commits[id]
	if !ok {
		return nil, &domain.Error{Code: 404, Message: "commit not found"}
	}
	return &domain.CommitDetail{ID: id, Tree: flatTree(files)}, nil
}

func (f *fakeDiffClient) GetMergeBase(context.Context, string, string, domain.MergeRef, domain.MergeRef) (*domain.MergeBaseInfo, error) {
	if f.mergeBase == "" {
		return nil, nil
	}
	return &domain.MergeBaseInfo{MergeBaseCommitID: f.mergeBase, MergeBaseTree: flatTree(f.commits[f.mergeBase])}, nil
}

func (f *fakeDiffClient) DownloadChunks(_ context.Context, _ domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(serverDomain.Hash, []byte) error) error {
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

func flatTree(files map[string]string) *serverDomain.TreeNode {
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

func diffUseCases(client *fakeDiffClient) func() UseCases {
	return func() UseCases {
		return UseCases{Diff: usecase.NewDiff(loggedInAuth(), client, localrepo.NewLocalRepo())}
	}
}

func TestAssetDiff_ImageWithThumbnails(t *testing.T) {
	oldPNG := testPNG(t, 4, 4, color.NRGBA{R: 255, A: 255})
	root := setupRepo(t, map[string]string{"tex.png": string(oldPNG)})
	writeFile(t, root, "tex.png", string(testPNG(t, 8, 4, color.NRGBA{B: 255, A: 255})))
	session := connect(t, Options{NewUseCases: offlineUseCases})

	res := call(t, session, "nipa_asset_diff", map[string]any{"repo": root, "path": "/tex.png", "include_images": true})
	out := decode[assetdiff.Result](t, res)
	require.Equal(t, assetdiff.StatusModified, out.Status)
	require.NotNil(t, out.Image)
	require.Equal(t, 4, out.Image.Old.Width)
	require.Equal(t, 8, out.Image.New.Width)
	require.Empty(t, out.Image.Old.ThumbnailPNG)
	images := 0
	for _, c := range res.Content {
		if img, ok := c.(*mcp.ImageContent); ok {
			images++
			require.Equal(t, "image/png", img.MIMEType)
			_, err := png.Decode(bytes.NewReader(img.Data))
			require.NoError(t, err)
		}
	}
	require.Equal(t, 2, images)
	require.NotContains(t, toolText(t, res), "thumbnail_png")

	plain := call(t, session, "nipa_asset_diff", map[string]any{"repo": root, "path": "tex.png"})
	require.Len(t, plain.Content, 1)
	require.NotNil(t, decode[assetdiff.Result](t, plain).Image.Compare)
}

func TestAssetDiff_SidesAndErrors(t *testing.T) {
	root := setupRepo(t, map[string]string{"data.csv": "id,name\n1,a\n", "old.txt": "one\n"})
	writeFile(t, root, "data.csv", "id,name\n1,b\n2,c\n")
	writeFile(t, root, "new.txt", "one\ntwo\n")
	session := connect(t, Options{NewUseCases: offlineUseCases})

	table := decode[assetdiff.Result](t, call(t, session, "nipa_asset_diff", map[string]any{"repo": root, "path": "data.csv", "max_rows": 1}))
	require.NotNil(t, table.Table)
	sheet := decode[assetdiff.Result](t, call(t, session, "nipa_asset_diff", map[string]any{"repo": root, "path": "data.csv", "sheet": " Items "}))
	require.Equal(t, `no sheet named "Items"`, sheet.Error)

	renamed := decode[assetdiff.Result](t, call(t, session, "nipa_asset_diff", map[string]any{"repo": root, "path": "new.txt", "old_path": "old.txt"}))
	require.Equal(t, "old.txt", renamed.OldPath)
	require.Equal(t, assetdiff.StatusModified, renamed.Status)

	same := decode[assetdiff.Result](t, call(t, session, "nipa_asset_diff", map[string]any{"repo": root, "path": "old.txt", "old": "working", "new": "snapshot", "include_images": true}))
	require.Equal(t, assetdiff.StatusUnchanged, same.Status)

	added := decode[assetdiff.Result](t, call(t, session, "nipa_asset_diff", map[string]any{"repo": root, "path": "new.txt"}))
	require.Equal(t, assetdiff.StatusAdded, added.Status)

	cases := []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"repo": root, "path": " "}, "a path is required"},
		{map[string]any{"repo": root, "path": "nope.bin"}, "nope.bin does not exist in snapshot or working"},
		{map[string]any{"repo": root, "path": "../x"}, "x does not exist"},
		{map[string]any{"repo": filepath.Join(t.TempDir(), "missing"), "path": "a"}, "not a nipa repository"},
	}
	for _, tc := range cases {
		res := call(t, session, "nipa_asset_diff", tc.args)
		require.True(t, res.IsError, tc.want)
		require.Contains(t, strings.ToLower(toolText(t, res)), strings.ToLower(tc.want))
	}

	unconfigured := connect(t, Options{NewUseCases: func() UseCases { return UseCases{} }})
	res := call(t, unconfigured, "nipa_asset_diff", map[string]any{"repo": root, "path": "old.txt"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "diff is not configured")

	offline := connect(t, Options{NewUseCases: diffUseCases(&fakeDiffClient{err: errors.New("server down")})})
	res = call(t, offline, "nipa_asset_diff", map[string]any{"repo": root, "path": "old.txt", "old": "main"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "server down")
}

func TestImageResult_NoThumbnails(t *testing.T) {
	res := &assetdiff.Result{Path: "a.png", Image: &assetdiff.ImageResult{New: &assetdiff.ImageSide{Width: 1}}}
	call, out, err := imageResult(res)
	require.NoError(t, err)
	require.Nil(t, call)
	require.Same(t, res, out)
}

func TestDescribeChanges_WorkingCopy(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "one\n", "b.txt": "gone\n", "tex.png": string(testPNG(t, 2, 2, color.NRGBA{A: 255}))})
	writeFile(t, root, "a.txt", "one\ntwo\n")
	writeFile(t, root, "tex.png", string(testPNG(t, 4, 2, color.NRGBA{A: 255})))
	require.NoError(t, os.Remove(filepath.Join(root, "b.txt")))
	writeFile(t, root, "c.txt", "new\n")
	stageFile(t, root, "c.txt")
	session := connect(t, Options{NewUseCases: offlineUseCases})

	out := decode[describeOutput](t, call(t, session, "nipa_describe_changes", map[string]any{"repo": root}))
	require.Equal(t, "snapshot", out.Old)
	require.Equal(t, "working", out.New)
	byPath := map[string]assetdiff.Summary{}
	for _, f := range out.Files {
		byPath[f.Path] = f
	}
	require.Equal(t, assetdiff.StatusModified, byPath["a.txt"].Status)
	require.Equal(t, assetdiff.StatusRemoved, byPath["b.txt"].Status)
	require.Equal(t, assetdiff.StatusAdded, byPath["c.txt"].Status)
	require.Contains(t, byPath["tex.png"].Headline, "4")

	capped := decode[describeOutput](t, call(t, session, "nipa_describe_changes", map[string]any{"repo": root, "max_files": 1}))
	require.Len(t, capped.Files, 1)
	require.Equal(t, 3, capped.Omitted)

	staged := decode[describeOutput](t, call(t, session, "nipa_describe_changes", map[string]any{"repo": root, "staged": true}))
	require.Len(t, staged.Files, 1)
	require.Equal(t, "c.txt", staged.Files[0].Path)

	for _, args := range []map[string]any{
		{"repo": root, "revisions": []string{"a", "b", "c"}},
		{"repo": root, "revisions": []string{"a"}, "staged": true},
		{"repo": root, "merge_base": true},
	} {
		res := call(t, session, "nipa_describe_changes", args)
		require.True(t, res.IsError)
	}
	res := call(t, connect(t, Options{NewUseCases: func() UseCases { return UseCases{} }}), "nipa_describe_changes", map[string]any{"repo": root})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "diff is not configured")
}

func revisionClient() *fakeDiffClient {
	return &fakeDiffClient{
		branches: map[string]string{"main": "a", "feature": "b"},
		commits: map[string]map[string]string{
			"a": {"a.txt": "one\n", "old.txt": "same body\nline two\nline three\n"},
			"b": {"a.txt": "one\ntwo\n", "new.txt": "same body\nline two\nline three\n", "c.txt": "c\n"},
			"9": {"a.txt": "base\n"},
		},
	}
}

func TestDescribeChanges_Revisions(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "one\n"})
	writeFile(t, root, "a.txt", "one\nlocal\n")
	client := revisionClient()
	session := connect(t, Options{NewUseCases: diffUseCases(client)})

	one := decode[describeOutput](t, call(t, session, "nipa_describe_changes", map[string]any{"repo": root, "revisions": []string{"feature"}}))
	require.Equal(t, "feature", one.Old)
	require.Equal(t, "working", one.New)
	require.NotEmpty(t, one.Files)

	two := decode[describeOutput](t, call(t, session, "nipa_describe_changes", map[string]any{"repo": root, "revisions": []string{"main", "feature"}}))
	require.Equal(t, "main", two.Old)
	require.Equal(t, "feature", two.New)
	statuses := map[string]assetdiff.Summary{}
	for _, f := range two.Files {
		statuses[f.Path] = f
	}
	require.Equal(t, assetdiff.StatusModified, statuses["a.txt"].Status)
	require.Equal(t, assetdiff.StatusAdded, statuses["c.txt"].Status)
	require.Equal(t, "renamed", statuses["new.txt"].Status)
	require.Equal(t, "old.txt", statuses["new.txt"].OldPath)
	require.Empty(t, statuses["new.txt"].Error)

	client.mergeBase = "9"
	base := decode[describeOutput](t, call(t, session, "nipa_describe_changes", map[string]any{"repo": root, "revisions": []string{"main", "feature"}, "merge_base": true}))
	require.Equal(t, "9", base.Old)

	client.mergeBase = ""
	unrelated := decode[describeOutput](t, call(t, session, "nipa_describe_changes", map[string]any{"repo": root, "revisions": []string{"main", "feature"}, "merge_base": true}))
	require.Empty(t, unrelated.Old)
	for _, f := range unrelated.Files {
		require.Equal(t, assetdiff.StatusAdded, f.Status, f.Path)
	}

	res := call(t, session, "nipa_describe_changes", map[string]any{"repo": root, "revisions": []string{"nope"}})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), `revision "nope" not found`)
}

type describeMRClient struct {
	recordingMRClient
	mr           *domain.MergeRequest
	mergeability *domain.Mergeability
	viewErr      error
}

func (d *describeMRClient) GetMergeRequest(context.Context, string, string, int64) (*domain.MergeRequest, *domain.Mergeability, error) {
	return d.mr, d.mergeability, d.viewErr
}

func TestMRDescribeChanges(t *testing.T) {
	root := setupRepo(t, nil)
	mrClient := &describeMRClient{
		mr:           &domain.MergeRequest{Number: 3, SourceBranch: "feature", TargetBranch: "main"},
		mergeability: &domain.Mergeability{SourceCommitID: "b", MergeBaseCommitID: "a"},
	}
	mrClient.diff = []*domain.MergeRequestDiffFile{
		{Path: "a.txt", Status: "M"},
		{Path: "c.txt", Status: "A"},
		{Path: "new.txt", OldPath: "old.txt", Status: "R"},
		nil,
	}
	diffClient := revisionClient()
	factory := func() UseCases {
		return UseCases{
			MR:   usecase.NewMergeRequest(loggedInAuth(), mrClient, localrepo.NewLocalRepo()),
			Diff: usecase.NewDiff(loggedInAuth(), diffClient, localrepo.NewLocalRepo()),
		}
	}
	session := connect(t, Options{NewUseCases: factory})

	out := decode[mrDescribeOutput](t, call(t, session, "nipa_mr_describe_changes", map[string]any{"repo": root, "number": 3}))
	require.Equal(t, int64(3), out.Number)
	require.Equal(t, mrSide{Revision: "a", Kind: "merge_base"}, out.Base)
	require.Equal(t, mrSide{Revision: "b", Kind: "source_head"}, out.Head)
	require.Len(t, out.Files, 3)
	for _, f := range out.Files {
		require.Empty(t, f.Error, f.Path)
		require.NotEmpty(t, f.Headline, f.Path)
	}

	mrClient.viewErr = errors.New("view failed")
	res := call(t, session, "nipa_mr_describe_changes", map[string]any{"repo": root, "number": 3})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "view failed")
	mrClient.viewErr = nil
	mrClient.err = errors.New("diff failed")
	res = call(t, session, "nipa_mr_describe_changes", map[string]any{"repo": root, "number": 3})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "diff failed")

	noDiff := connect(t, Options{NewUseCases: func() UseCases {
		return UseCases{MR: usecase.NewMergeRequest(loggedInAuth(), mrClient, localrepo.NewLocalRepo())}
	}})
	res = call(t, noDiff, "nipa_mr_describe_changes", map[string]any{"repo": root, "number": 3})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "diff is not configured")
	res = call(t, connect(t, Options{NewUseCases: func() UseCases { return UseCases{} }}), "nipa_mr_describe_changes", map[string]any{"repo": root, "number": 3})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "merge requests are not configured")
}

func TestMRSides(t *testing.T) {
	base, head := mrSides(nil, nil)
	require.Equal(t, mrSide{Kind: "target_branch"}, base)
	require.Equal(t, mrSide{Kind: "source_branch"}, head)

	mr := &domain.MergeRequest{SourceBranch: "f", TargetBranch: "m", MergeBaseCommitID: "x", Review: &domain.MergeRequestReviewState{HeadCommitID: "h"}}
	base, head = mrSides(mr, nil)
	require.Equal(t, mrSide{Revision: "x", Kind: "merge_base"}, base)
	require.Equal(t, mrSide{Revision: "h", Kind: "source_head"}, head)

	base, _ = mrSides(&domain.MergeRequest{TargetBranch: "m"}, &domain.Mergeability{TargetCommitID: "t"})
	require.Equal(t, mrSide{Revision: "t", Kind: "target_head"}, base)
	base, head = mrSides(&domain.MergeRequest{SourceBranch: "f", TargetBranch: "m"}, &domain.Mergeability{})
	require.Equal(t, mrSide{Revision: "m", Kind: "target_branch"}, base)
	require.Equal(t, mrSide{Revision: "f", Kind: "source_branch"}, head)
}

func TestMetaWarnings(t *testing.T) {
	unity := setupRepo(t, map[string]string{"ProjectSettings/ProjectVersion.txt": "m_EditorVersion: 2022.3.0f1\n"})
	writeFile(t, unity, "Assets/tex.png", "png")
	session := connect(t, Options{NewUseCases: offlineUseCases})

	out := decode[metaWarningsOutput](t, call(t, session, "nipa_meta_warnings", map[string]any{"repo": unity}))
	require.Equal(t, "unity", out.Engine)
	require.NotEmpty(t, out.Warnings)
	require.Equal(t, "missing-meta", out.Warnings[0].Kind)

	forced := decode[metaWarningsOutput](t, call(t, session, "nipa_meta_warnings", map[string]any{"repo": unity, "engine": "Godot"}))
	require.Equal(t, "godot", forced.Engine)
	require.Empty(t, forced.Warnings)

	godot := setupRepo(t, map[string]string{"project.godot": "config_version=5\n", "icon.png": "png", "icon.png.import": "[remap]\nuid=\"uid://abc\"\n"})
	require.NoError(t, os.Remove(filepath.Join(godot, "icon.png")))
	gout := decode[metaWarningsOutput](t, call(t, session, "nipa_meta_warnings", map[string]any{"repo": godot}))
	require.Equal(t, "godot", gout.Engine)
	require.NotEmpty(t, gout.Warnings)

	none := setupRepo(t, map[string]string{"a.txt": "a\n"})
	nout := decode[metaWarningsOutput](t, call(t, session, "nipa_meta_warnings", map[string]any{"repo": none}))
	require.Empty(t, nout.Engine)
	require.Empty(t, nout.Warnings)

	res := call(t, session, "nipa_meta_warnings", map[string]any{"repo": none, "engine": "unreal"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "unknown engine")
	res = call(t, session, "nipa_meta_warnings", map[string]any{"repo": filepath.Join(t.TempDir(), "missing")})
	require.True(t, res.IsError)
	res = call(t, connect(t, Options{NewUseCases: func() UseCases { return UseCases{} }}), "nipa_meta_warnings", map[string]any{"repo": none})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "diff is not configured")
}

func TestAssetRefsAndImpact(t *testing.T) {
	tex, mat := strings.Repeat("a", 32), strings.Repeat("b", 32)
	yaml := func(guid string) string {
		return "%YAML 1.1\n--- !u!21 &1\nMaterial:\n  t: {fileID: 1, guid: " + guid + ", type: 3}\n"
	}
	root := setupRepo(t, map[string]string{
		"Assets/hero.png": "png", "Assets/hero.png.meta": "guid: " + tex + "\n",
		"Assets/hero.mat": yaml(tex), "Assets/hero.mat.meta": "guid: " + mat + "\n",
		"Assets/Hero.prefab": yaml(mat),
	})
	session := connect(t, Options{NewUseCases: offlineUseCases})

	refs := decode[assetscan.RefsReport](t, call(t, session, "nipa_asset_refs", map[string]any{"repo": root, "path": "/Assets/hero.mat"}))
	require.Equal(t, "Assets/hero.mat", refs.Path)
	require.Equal(t, []string{"Assets/Hero.prefab"}, refs.UsedBy)
	require.Len(t, refs.Uses, 1)
	require.Equal(t, "Assets/hero.png", refs.Uses[0].Path)

	none := decode[assetscan.RefsReport](t, call(t, session, "nipa_asset_refs", map[string]any{"repo": root, "path": "nope.png"}))
	require.NotNil(t, none.Uses)
	require.Empty(t, none.UsedBy)

	capped := decode[assetscan.RefsReport](t, call(t, session, "nipa_asset_refs", map[string]any{"repo": root, "path": "Assets/hero.mat", "max_files": 1}))
	require.True(t, capped.Truncated)
	require.Equal(t, 1, capped.Scanned)

	clean := decode[assetrefs.Impact](t, call(t, session, "nipa_asset_impact", map[string]any{"repo": root}))
	require.NotNil(t, clean.Broken)
	require.Empty(t, clean.Broken)

	for _, p := range []string{"Assets/hero.png", "Assets/hero.png.meta"} {
		require.NoError(t, os.Remove(filepath.Join(root, filepath.FromSlash(p))))
	}
	impact := decode[assetrefs.Impact](t, call(t, session, "nipa_asset_impact", map[string]any{"repo": root}))
	require.Len(t, impact.Broken, 1)
	require.Equal(t, assetrefs.BrokenRef{From: "Assets/hero.mat", Raw: "guid:" + tex, Target: "Assets/hero.png", Reason: "removed"}, impact.Broken[0])
	staged := decode[assetrefs.Impact](t, call(t, session, "nipa_asset_impact", map[string]any{"repo": root, "staged": true}))
	require.Empty(t, staged.Broken)

	res := call(t, session, "nipa_asset_refs", map[string]any{"repo": root, "path": " "})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "a path is required")
	missing := filepath.Join(t.TempDir(), "missing")
	require.True(t, call(t, session, "nipa_asset_refs", map[string]any{"repo": missing, "path": "a"}).IsError)
	require.True(t, call(t, session, "nipa_asset_impact", map[string]any{"repo": missing}).IsError)
	empty := connect(t, Options{NewUseCases: func() UseCases { return UseCases{} }})
	require.True(t, call(t, empty, "nipa_asset_refs", map[string]any{"repo": root, "path": "a"}).IsError)
	require.True(t, call(t, empty, "nipa_asset_impact", map[string]any{"repo": root}).IsError)
}

func TestReadSnapshotSmall(t *testing.T) {
	root := setupRepo(t, map[string]string{"big.meta": strings.Repeat("x", maxMetaBytes+1), "ok.meta": "guid: 1\n"})
	d := usecase.NewDiff(nil, nil, localrepo.NewLocalRepo())
	data, err := readSnapshotSmall(context.Background(), d, root, "ok.meta")
	require.NoError(t, err)
	require.Equal(t, "guid: 1\n", string(data))
	_, err = readSnapshotSmall(context.Background(), d, root, "big.meta")
	require.ErrorIs(t, err, errMetaTooLarge)
}

func TestMetaWarnings_GUIDBudget(t *testing.T) {
	files := map[string]string{"ProjectSettings/ProjectVersion.txt": "v\n"}
	for i := 0; i < maxGUIDChecks+2; i++ {
		name := fmt.Sprintf("Assets/f%03d.png", i)
		files[name] = "png"
		files[name+".meta"] = fmt.Sprintf("guid: %032d\n", i)
	}
	root := setupRepo(t, files)
	for i := 0; i < maxGUIDChecks+2; i++ {
		writeFile(t, root, fmt.Sprintf("Assets/f%03d.png.meta", i), fmt.Sprintf("guid: %032d\n", i+1000))
	}
	session := connect(t, Options{NewUseCases: offlineUseCases})
	out := decode[metaWarningsOutput](t, call(t, session, "nipa_meta_warnings", map[string]any{"repo": root}))
	changed := 0
	for _, w := range out.Warnings {
		if w.Kind == "guid-changed" {
			changed++
		}
	}
	require.LessOrEqual(t, changed, maxGUIDChecks)
	require.NotZero(t, changed)
}

func godotMergeClient(theirs string) *fakeDiffClient {
	return &fakeDiffClient{commits: map[string]map[string]string{
		"1": {"level.tscn": godotBase, "broken.tscn": "[node name=\"A\"\n"},
		"2": {"level.tscn": godotOurs, "broken.tscn": godotOurs},
		"3": {"level.tscn": theirs, "broken.tscn": godotTheirs},
	}}
}

func TestGodotMerge(t *testing.T) {
	root := setupRepo(t, map[string]string{"level.tscn": "conflicted\n", "art.png": "png", "broken.tscn": "x\n"})
	require.NoError(t, os.Chmod(filepath.Join(root, "level.tscn"), 0o444))
	client := godotMergeClient(godotTheirs)
	session := connect(t, Options{Allow: map[string]bool{CapMerge: true}, NewUseCases: diffUseCases(client)})

	res := call(t, session, "nipa_godot_merge", map[string]any{"repo": root, "path": "level.tscn"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "no merge or revert is waiting")

	saveMergeState(t, root, &domain.MergeState{
		SourceBranch: "feature", SourceCommitID: "3", BaseCommitID: "1", TargetCommitID: "2",
		Conflicts:      []string{"level.tscn", "broken.tscn", "res.tres"},
		ConflictBinary: []string{"res.tres"},
	})
	set := decode[conflictsOutput](t, call(t, session, "nipa_conflicts", map[string]any{"repo": root}))
	require.Equal(t, "2", set.Ours.ID)

	cases := []struct {
		path, want string
	}{
		{"art.png", "is not a Godot text scene"},
		{"", "is not a Godot text scene"},
		{"other.tscn", "has no conflict to resolve"},
		{"res.tres", "is stored as binary"},
		{"broken.tscn", "cannot be merged"},
	}
	for _, tc := range cases {
		res := call(t, session, "nipa_godot_merge", map[string]any{"repo": root, "path": tc.path})
		require.True(t, res.IsError, tc.path)
		require.Contains(t, toolText(t, res), tc.want, tc.path)
	}

	client.commits["3"]["level.tscn"] = godotOther
	conflicted := decode[godotMergeOutput](t, call(t, session, "nipa_godot_merge", map[string]any{"repo": root, "path": "level.tscn"}))
	require.False(t, conflicted.Clean)
	require.NotEmpty(t, conflicted.Conflicts)
	require.Nil(t, conflicted.Status)
	require.NotEmpty(t, conflicted.Next)
	data, err := os.ReadFile(filepath.Join(root, "level.tscn"))
	require.NoError(t, err)
	require.Equal(t, "conflicted\n", string(data))

	client.commits["3"]["level.tscn"] = godotTheirs
	clean := decode[godotMergeOutput](t, call(t, session, "nipa_godot_merge", map[string]any{"repo": root, "path": "/level.tscn"}))
	require.True(t, clean.Clean)
	require.Empty(t, clean.Conflicts)
	require.NotNil(t, clean.Status)
	require.Contains(t, clean.Status.Staged, "level.tscn")
	data, err = os.ReadFile(filepath.Join(root, "level.tscn"))
	require.NoError(t, err)
	require.Contains(t, string(data), "speed = 7")
	require.Contains(t, string(data), "visible = false")
	info, err := os.Stat(filepath.Join(root, "level.tscn"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o444), info.Mode().Perm())

	res = call(t, connect(t, Options{Allow: map[string]bool{CapMerge: true}, NewUseCases: func() UseCases { return UseCases{} }}),
		"nipa_godot_merge", map[string]any{"repo": root, "path": "broken.tscn"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "diff is not configured")
	res = call(t, session, "nipa_godot_merge", map[string]any{"repo": filepath.Join(t.TempDir(), "missing"), "path": "a.tscn"})
	require.True(t, res.IsError)
}

func TestGodotMerge_TheirsDeletedAndOffline(t *testing.T) {
	root := setupRepo(t, map[string]string{"level.tscn": "x\n"})
	client := godotMergeClient(godotTheirs)
	delete(client.commits["3"], "level.tscn")
	session := connect(t, Options{Allow: map[string]bool{CapMerge: true}, NewUseCases: diffUseCases(client)})
	saveMergeState(t, root, &domain.MergeState{SourceBranch: "feature", SourceCommitID: "3", BaseCommitID: "1", TargetCommitID: "2", Conflicts: []string{"level.tscn"}})

	out := decode[godotMergeOutput](t, call(t, session, "nipa_godot_merge", map[string]any{"repo": root, "path": "level.tscn"}))
	require.False(t, out.Clean)
	require.Contains(t, out.Conflicts[0].Message, "theirs deleted the file")

	client.err = errors.New("server down")
	res := call(t, session, "nipa_godot_merge", map[string]any{"repo": root, "path": "level.tscn"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "server down")
}

func TestCleanPath(t *testing.T) {
	require.Equal(t, "a/b", cleanPath(" /a//b/ "))
	require.Empty(t, cleanPath("  "))
}

func TestAssetCheck(t *testing.T) {
	root := setupRepo(t, map[string]string{"tex.png": string(testPNG(t, 4, 4, color.NRGBA{R: 255, A: 255}))})
	writeFile(t, root, "tex.png", string(testPNG(t, 6, 4, color.NRGBA{B: 255, A: 255})))
	session := connect(t, Options{NewUseCases: offlineUseCases})

	out := decode[assetscan.Report](t, call(t, session, "nipa_asset_check", map[string]any{"repo": root}))
	require.False(t, out.Config)
	require.Empty(t, out.Violations)

	writeFile(t, root, ".nipaassets.yml", "rules:\n  - name: textures\n    paths: ['*.png']\n    power_of_two: true\n")
	out = decode[assetscan.Report](t, call(t, session, "nipa_asset_check", map[string]any{"repo": root}))
	require.True(t, out.Config)
	require.Equal(t, 1, out.Checked)
	require.Len(t, out.Violations, 1)
	require.Equal(t, "tex.png", out.Violations[0].Path)
	require.Equal(t, "power_of_two", out.Violations[0].Limit)
	require.Equal(t, "textures", out.Violations[0].Rule)

	res := call(t, session, "nipa_asset_check", map[string]any{"repo": root, "revisions": []string{"a"}, "staged": true})
	require.True(t, res.IsError)

	writeFile(t, root, ".nipaassets.yml", "bogus: true\n")
	res = call(t, session, "nipa_asset_check", map[string]any{"repo": root})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "field bogus not found")

	res = call(t, session, "nipa_asset_check", map[string]any{"repo": t.TempDir()})
	require.True(t, res.IsError)
}
