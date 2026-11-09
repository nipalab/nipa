package assetscan

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/assetbudget"
	"github.com/nipalab/nipa/internal/assetdiff"
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const budget = `rules:
  - name: textures
    paths: ["*.png"]
    max_texture_size: 64
    power_of_two: true
  - name: audio
    paths: ["*.wav"]
    max_audio_seconds: 0.5
  - name: size
    paths: ["big/"]
    max_bytes: 4
  - name: names
    paths: ["**"]
    name_pattern: "^[a-z0-9_./]+$"
`

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	img.SetNRGBA(0, 0, color.NRGBA{R: 1, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func wavBytes(seconds float64) []byte {
	const rate = 8000
	n := int(seconds * rate)
	var data bytes.Buffer
	for i := 0; i < n; i++ {
		_ = binary.Write(&data, binary.LittleEndian, int16(math.Sin(float64(i)/10)*8000))
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+data.Len()))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(data.Len()))
	b.Write(data.Bytes())
	return b.Bytes()
}

func tree(files map[string]string) (*serverDomain.TreeNode, []*serverDomain.ChunkData) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	node := &serverDomain.TreeNode{Hash: chunker.Sum([]byte("snapshot"))}
	var pending []*serverDomain.ChunkData
	for _, name := range names {
		content := []byte(files[name])
		chunks, _ := chunker.ChunkAll(content)
		hashes := make([]serverDomain.Hash, len(chunks))
		fileChunks := make([]serverDomain.Chunk, len(chunks))
		for i, c := range chunks {
			hashes[i] = c.Hash
			fileChunks[i] = serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))}
			pending = append(pending, &serverDomain.ChunkData{Hash: c.Hash, Data: c.Data})
		}
		node.FileChildren = append(node.FileChildren, &serverDomain.File{
			Name: name, Mode: 2, SizeBytes: int64(len(content)), IsBinary: chunker.IsBinary(content),
			Hash: chunker.FileHash(hashes), Chunks: fileChunks,
		})
	}
	return node, pending
}

func setupRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	node, pending := tree(files)
	require.NoError(t, lr.StoreChunks(pending))
	require.NoError(t, lr.SaveTree(node))
	require.NoError(t, lr.Close())
	for name, content := range files {
		writeFile(t, root, name, content)
	}
	return root
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func stage(t *testing.T, root, p string) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	defer func() { _ = lr.Close() }()
	require.NoError(t, lr.StageAdd(p))
}

func offlineDiff() *usecase.Diff {
	return usecase.NewDiff(nil, nil, localrepo.NewLocalRepo())
}

func limits(report *Report) []string {
	var out []string
	for _, v := range report.Violations {
		out = append(out, v.Path+" "+v.Limit+" "+v.Rule)
	}
	return out
}

func TestCheck_WorkingCopy(t *testing.T) {
	root := setupRepo(t, map[string]string{"tex.png": string(pngBytes(t, 4, 4)), "ok.txt": "a\n", "gone.png": "x"})
	writeFile(t, root, "tex.png", string(pngBytes(t, 100, 30)))
	writeFile(t, root, "voice.wav", string(wavBytes(1)))
	writeFile(t, root, "Bad.txt", "x\n")
	writeFile(t, root, "big/blob.bin", "0123456789")
	writeFile(t, root, "broken.png", "not a png")
	require.NoError(t, os.Remove(filepath.Join(root, "gone.png")))
	for _, p := range []string{"voice.wav", "Bad.txt", "big/blob.bin", "broken.png"} {
		stage(t, root, p)
	}

	report, err := Check(context.Background(), offlineDiff(), root, Request{})
	require.NoError(t, err)
	require.False(t, report.Config)
	require.Empty(t, report.Violations)

	writeFile(t, root, assetbudget.FileName, budget)
	report, err = Check(context.Background(), offlineDiff(), root, Request{})
	require.NoError(t, err)
	require.True(t, report.Config)
	assert.Equal(t, SideSnapshot, report.Old)
	assert.Equal(t, SideWorking, report.New)
	assert.Equal(t, []string{
		"Bad.txt name_pattern names",
		"big/blob.bin max_bytes size",
		"tex.png max_texture_size textures",
		"tex.png power_of_two textures",
		"voice.wav max_audio_seconds audio",
	}, limits(report))
	assert.Equal(t, 5, report.Checked)
	require.Len(t, report.Skipped, 1)
	assert.Equal(t, "broken.png", report.Skipped[0].Path)

	staged, err := Check(context.Background(), offlineDiff(), root, Request{Staged: true, Paths: []string{"big"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"big/blob.bin max_bytes size"}, limits(staged))
}

func TestCheck_BadConfigAndDiffError(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, assetbudget.FileName, "nope: 1\n")
	_, err := Check(context.Background(), offlineDiff(), root, Request{})
	require.ErrorContains(t, err, "field nope not found")

	writeFile(t, root, assetbudget.FileName, budget)
	_, err = Check(context.Background(), offlineDiff(), root, Request{MergeBase: true})
	require.Error(t, err)
}

func TestCheckChanges_LoaderOutcomes(t *testing.T) {
	cfg, err := assetbudget.Parse([]byte(budget))
	require.NoError(t, err)
	changes := &Changes{Files: []Change{
		{Item: ChangeItem("missing.png", "", "A", "", SideWorking)},
		{Item: ChangeItem("fail.png", "", "A", "", SideWorking)},
		{Item: ChangeItem("huge.png", "", "M", SideSnapshot, SideWorking), NewSize: 1 << 40},
		{Item: ChangeItem("deleted.png", "", "D", SideSnapshot, SideWorking)},
		{Item: ChangeItem("UPPER", "", "A", "", SideWorking)},
	}}
	load := func(_ context.Context, ref assetdiff.Ref, _ int64) ([]byte, error) {
		switch ref.Path {
		case "missing.png":
			return nil, assetdiff.ErrMissing
		case "huge.png":
			return nil, assetdiff.ErrTooLarge
		}
		return nil, errors.New("disk on fire")
	}
	report, err := CheckChanges(context.Background(), load, cfg, changes)
	require.NoError(t, err)
	assert.Equal(t, 4, report.Checked)
	assert.Equal(t, []Skip{
		{Path: "missing.png", Reason: "the file is missing"},
		{Path: "fail.png", Reason: "disk on fire"},
		{Path: "huge.png", Reason: "the file is too large to compare"},
	}, report.Skipped)
	assert.Equal(t, []string{"UPPER name_pattern names"}, limits(report))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = CheckChanges(ctx, load, cfg, changes)
	require.ErrorIs(t, err, context.Canceled)

	cancelling, stop := context.WithCancel(context.Background())
	defer stop()
	_, err = CheckChanges(cancelling, func(context.Context, assetdiff.Ref, int64) ([]byte, error) {
		stop()
		return nil, errors.New("cancelled")
	}, cfg, changes)
	require.ErrorIs(t, err, context.Canceled)
}

func TestCheckChanges_ProvenanceFromNewSide(t *testing.T) {
	cfg, err := assetbudget.Parse([]byte("rules:\n  - name: gen\n    paths: [\"art/**\"]\n    require_provenance: true\n"))
	require.NoError(t, err)
	changes := &Changes{Old: "v1", New: "HEAD", Files: []Change{
		{Item: ChangeItem("art/a.png", "", "A", "v1", "HEAD")},
		{Item: ChangeItem("art/b.png", "", "A", "v1", "HEAD")},
	}}
	var sides []assetdiff.Ref
	files := map[string]string{}
	load := func(_ context.Context, ref assetdiff.Ref, _ int64) ([]byte, error) {
		sides = append(sides, ref)
		content, ok := files[ref.Path]
		if !ok {
			return nil, assetdiff.ErrMissing
		}
		if content == "fail" {
			return nil, errors.New("offline")
		}
		return []byte(content), nil
	}
	report, err := CheckChanges(context.Background(), load, cfg, changes)
	require.NoError(t, err)
	assert.Equal(t, []string{"art/a.png require_provenance gen", "art/b.png require_provenance gen"}, limits(report))
	require.Equal(t, []assetdiff.Ref{{Source: "revision", Revision: "HEAD", Path: ".nipaprovenance.yml"}}, sides)

	files[".nipaprovenance.yml"] = "version: 1\nfiles:\n  art/a.png: {tool: x}\n"
	report, err = CheckChanges(context.Background(), load, cfg, changes)
	require.NoError(t, err)
	assert.Equal(t, []string{"art/b.png require_provenance gen"}, limits(report))

	files[".nipaprovenance.yml"] = "version: 9\n"
	_, err = CheckChanges(context.Background(), load, cfg, changes)
	require.ErrorContains(t, err, ".nipaprovenance.yml: unsupported version 9")
	files[".nipaprovenance.yml"] = "fail"
	_, err = CheckChanges(context.Background(), load, cfg, changes)
	require.ErrorContains(t, err, "offline")

	sides = nil
	plain, err := assetbudget.Parse([]byte(budget))
	require.NoError(t, err)
	_, err = CheckChanges(context.Background(), load, plain, &Changes{New: "HEAD", Files: []Change{{Item: ChangeItem("x.txt", "", "A", "", "HEAD")}}})
	require.NoError(t, err)
	require.Empty(t, sides, "the provenance file is only read when a rule requires it")
}

func TestBlobLoader(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "0123456789"})
	load := BlobLoader(offlineDiff(), root)
	_, err := load(context.Background(), assetdiff.Ref{Source: "working", Path: "a.txt"}, 4)
	require.ErrorIs(t, err, assetdiff.ErrTooLarge)
	data, err := load(context.Background(), assetdiff.Ref{Source: "snapshot", Path: "a.txt"}, 64)
	require.NoError(t, err)
	require.Equal(t, "0123456789", string(data))
	_, err = load(context.Background(), assetdiff.Ref{Source: "snapshot", Path: "zz.txt"}, 64)
	require.ErrorIs(t, err, assetdiff.ErrMissing)
}

func TestSideRefAndChangeItem(t *testing.T) {
	require.Equal(t, &assetdiff.Ref{Source: "working", Path: "a"}, SideRef(" working ", "a"))
	require.Equal(t, &assetdiff.Ref{Source: "snapshot", Path: "a"}, SideRef("snapshot", "a"))
	require.Equal(t, &assetdiff.Ref{Source: "revision", Revision: "HEAD", Path: "a"}, SideRef("HEAD", "a"))

	it := ChangeItem("a", "", "D", "main", "working")
	require.Equal(t, assetdiff.StatusRemoved, it.Status)
	require.Nil(t, it.New)
	require.Equal(t, &assetdiff.Ref{Source: "revision", Revision: "main", Path: "a"}, it.Old)

	it = ChangeItem("b", "a", "renamed", "snapshot", "working")
	require.Equal(t, "a", it.OldPath)
	require.Equal(t, &assetdiff.Ref{Source: "snapshot", Path: "a"}, it.Old)

	it = ChangeItem("b", "", "M", "", "x")
	require.Nil(t, it.Old)
	require.Equal(t, "x", it.New.Revision)
}

type fakeClient struct {
	branches  map[string]string
	commits   map[string]map[string]string
	mergeBase string
	err       error
}

func (f *fakeClient) Connect(context.Context, string) error { return nil }

func (f *fakeClient) GetBranchByName(_ context.Context, _, _, name string) (*serverDomain.Branch, error) {
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

func (f *fakeClient) GetTagByName(context.Context, string, string, string) (*domain.Tag, error) {
	return nil, &domain.Error{Code: 404, Message: "tag not found"}
}

func (f *fakeClient) GetCommit(_ context.Context, _, _, id string) (*domain.CommitDetail, error) {
	files, ok := f.commits[id]
	if !ok {
		return nil, &domain.Error{Code: 404, Message: "commit not found"}
	}
	node, _ := tree(files)
	return &domain.CommitDetail{ID: id, Tree: node}, nil
}

func (f *fakeClient) GetMergeBase(context.Context, string, string, domain.MergeRef, domain.MergeRef) (*domain.MergeBaseInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	node, _ := tree(f.commits[f.mergeBase])
	return &domain.MergeBaseInfo{MergeBaseCommitID: f.mergeBase, MergeBaseTree: node}, nil
}

func (f *fakeClient) DownloadChunks(_ context.Context, _ domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(serverDomain.Hash, []byte) error) error {
	all := map[serverDomain.Hash][]byte{}
	for _, files := range f.commits {
		_, pending := tree(files)
		for _, c := range pending {
			all[c.Hash] = c.Data
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

type fakeLogin struct{}

func (fakeLogin) LoginWithUsernamePassword(context.Context, string, string, string) (*domain.LoginResult, error) {
	return nil, errors.New("login not expected")
}

type fakeStorage struct{}

func (fakeStorage) SaveToken(*domain.LoginResult) error { return nil }

func (fakeStorage) LoadToken(string) (*domain.LoginResult, error) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":9999999999}`))
	return &domain.LoginResult{AccessToken: "e30." + payload + ".signature"}, nil
}

func TestCheck_Revisions(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, assetbudget.FileName, budget)
	client := &fakeClient{
		branches: map[string]string{"main": "1", "feature": "2"},
		commits: map[string]map[string]string{
			"1": {"tex.png": string(pngBytes(t, 4, 4))},
			"2": {"tex.png": string(pngBytes(t, 128, 128)), "Up.txt": "x"},
		},
		mergeBase: "1",
	}
	d := usecase.NewDiff(usecase.NewAuth(fakeLogin{}, fakeStorage{}, nil), client, localrepo.NewLocalRepo())

	report, err := Check(context.Background(), d, root, Request{Revisions: []string{"main", "feature"}, MergeBase: true})
	require.NoError(t, err)
	assert.Equal(t, "1", report.Old)
	assert.Equal(t, "feature", report.New)
	assert.Equal(t, []string{"Up.txt name_pattern names", "tex.png max_texture_size textures"}, limits(report))

	one, err := Check(context.Background(), d, root, Request{Revisions: []string{"feature"}})
	require.NoError(t, err)
	assert.Equal(t, "feature", one.Old)
	assert.Equal(t, SideWorking, one.New)

	client.err = errors.New("no merge base")
	_, err = Check(context.Background(), d, root, Request{Revisions: []string{"main", "feature"}, MergeBase: true})
	require.ErrorContains(t, err, "no merge base")
}
