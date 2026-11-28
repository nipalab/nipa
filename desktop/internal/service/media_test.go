package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

// workTree writes files into a fresh clone root (with a .nipa folder) and
// returns the root.
func workTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".nipa"), 0o755))
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	return root
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks need privileges on Windows")
		}
		require.NoError(t, err)
	}
}

func TestResolveWorkingFile(t *testing.T) {
	root := workTree(t, map[string]string{"art/a.png": "PNG", ".nipa/state.db": "secret", "dir/x": ""})
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o644))
	symlink(t, filepath.Join(outside, "secret.txt"), filepath.Join(root, "escape.txt"))
	symlink(t, outside, filepath.Join(root, "outdir"))
	symlink(t, filepath.Join(root, ".nipa", "state.db"), filepath.Join(root, "meta.db"))
	symlink(t, filepath.Join(root, "art", "a.png"), filepath.Join(root, "alias.png"))

	full, size, err := resolveWorkingFile(root, "art/a.png")
	require.NoError(t, err)
	require.EqualValues(t, 3, size)
	real, _ := filepath.EvalSymlinks(filepath.Join(root, "art", "a.png"))
	require.Equal(t, real, full)

	full, _, err = resolveWorkingFile(root, "alias.png")
	require.NoError(t, err, "a symlink that stays inside the clone is fine")
	require.Equal(t, real, full)
	_, _, err = resolveWorkingFile(root, "art/../art/a.png")
	require.NoError(t, err)

	for _, rel := range []string{
		"", ".", "..", "../x", "art/../../x", "/etc/passwd", filepath.Join(outside, "secret.txt"),
		".nipa/state.db", ".NIPA/state.db", "art/../.nipa/state.db", ".nipa",
		"escape.txt", "outdir/secret.txt", "meta.db", "dir", "missing.png",
	} {
		_, _, err := resolveWorkingFile(root, rel)
		require.ErrorIs(t, err, errNotWorkingFile, rel)
	}
	_, _, err = resolveWorkingFile("", "art/a.png")
	require.ErrorIs(t, err, errNotWorkingFile)
	_, _, err = resolveWorkingFile(filepath.Join(root, "nope"), "art/a.png")
	require.ErrorIs(t, err, errNotWorkingFile)
}

func TestMediaType(t *testing.T) {
	for name, want := range map[string]string{
		"a.MP4": "video/mp4", "hit.wav": "audio/wav", "hero.glb": "model/gltf-binary", "hero.gltf": "model/gltf+json",
		"ship.obj": "model/obj", "ship.mtl": "model/mtl", "a.fbx": "application/octet-stream", "a.usdz": "model/vnd.usdz+zip",
		"skin.PNG": "image/png", "t.jpg": "image/jpeg", "t.ktx2": "image/ktx2", "noext": "application/octet-stream", "x.weird": "application/octet-stream",
	} {
		require.Equal(t, want, mediaType(name), name)
	}
}

func TestMediaStoreNeverDeletesRegisteredFiles(t *testing.T) {
	root := workTree(t, map[string]string{"clip.mp4": "working bytes"})
	full, _, err := resolveWorkingFile(root, "clip.mp4")
	require.NoError(t, err)
	var m mediaStore
	defer func() { _ = m.close() }()

	first := m.register(full, "clip.mp4")
	f, ok := m.lookup(first, false)
	require.True(t, ok)
	require.False(t, f.owned)
	require.Equal(t, full, f.path)
	require.Empty(t, m.dir, "registering creates no temp folder")

	m.release([]string{first})
	for i := 0; i < 2*maxReleasedMedia; i++ {
		m.release([]string{m.register(full, "clip.mp4")})
	}
	_, ok = m.lookup(first, false)
	require.False(t, ok, "the token is evicted")
	m.drop(m.register(full, "clip.mp4"))
	held := m.register(full, "clip.mp4")
	written, err := m.write("copy.mp4", []byte("copy"))
	require.NoError(t, err)
	copied, _ := m.lookup(written, false)
	require.True(t, copied.owned)
	m.drop(written)
	_, err = os.Stat(copied.path)
	require.True(t, os.IsNotExist(err), "dropping a written file removes it")

	require.NoError(t, m.close())
	_, ok = m.lookup(held, false)
	require.False(t, ok)
	data, err := os.ReadFile(full)
	require.NoError(t, err)
	require.Equal(t, "working bytes", string(data), "the working file survives eviction, drop and close")
}

func TestMediaStoreWriteFails(t *testing.T) {
	m := mediaStore{dir: filepath.Join(t.TempDir(), "gone")}
	_, err := m.write("a.wav", []byte("x"))
	require.Error(t, err)
	require.Empty(t, m.files)
}

func TestMediaMiddlewareServesWorkingFileWithRanges(t *testing.T) {
	root := workTree(t, map[string]string{"art/hero.glb": "glTF0123456789"})
	svc, _ := newModels(t, nil)
	full, _, err := resolveWorkingFile(root, "art/hero.glb")
	require.NoError(t, err)
	token := svc.media.register(full, "art/hero.glb")
	url := mediaURL(token, "art/hero.glb")
	require.Equal(t, MediaPrefix+token+"/hero.glb", url)

	handler := MediaMiddleware(svc)(http.NotFoundHandler())
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Range", "bytes=4-7")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusPartialContent, rec.Code)
	require.Equal(t, "model/gltf-binary", rec.Header().Get("Content-Type"))
	body, _ := io.ReadAll(rec.Body)
	require.Equal(t, "0123", string(body))

	// A file that turned into a folder after registering is not served.
	require.NoError(t, os.Remove(full))
	require.NoError(t, os.Mkdir(full, 0o755))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestService_VideoDiffServesWorkingFileInPlace(t *testing.T) {
	mp4 := mediaFixture(t, "clip.mp4")
	root := workTree(t, map[string]string{"cut/intro.mp4": mp4})
	svc, d := newModels(t, map[string]string{"cut/intro.mp4": mp4, "cut/old.webm": mediaFixture(t, "clip.webm")})
	out, err := svc.VideoDiff(ctx, root, &ImageRef{Source: "snapshot", Path: "cut/old.webm"}, &ImageRef{Source: "working", Path: "cut/intro.mp4"})
	require.NoError(t, err)
	require.Len(t, d.reads, 1, "only the stored side goes through the daemon")
	require.Equal(t, "cut/old.webm", d.reads[0].GetBlob().GetPath())
	require.Equal(t, "H.264", out.New.Stats.VideoCodec)
	require.EqualValues(t, len(mp4), out.New.Size)

	f, ok := svc.media.lookup(out.New.Media, false)
	require.True(t, ok)
	real, _ := filepath.EvalSymlinks(filepath.Join(root, "cut", "intro.mp4"))
	require.Equal(t, real, f.path, "the working side is served where it is")
	require.False(t, f.owned)
	temps, err := os.ReadDir(svc.media.dir)
	require.NoError(t, err)
	require.Len(t, temps, 1, "only the stored side is copied")
	require.Equal(t, mp4, served(t, svc, out.New.URL))

	svc.ReleaseMedia([]string{out.New.Media, out.Old.Media})
	require.NoError(t, svc.ServiceShutdown())
	_, err = os.Stat(real)
	require.NoError(t, err, "closing the store keeps the working file")
}

func TestService_VideoDiffCopiesStoredSidesConcurrently(t *testing.T) {
	svc, d := newModels(t, map[string]string{"a.mp4": "old video", "b.mp4": "new video"})
	var started sync.WaitGroup
	started.Add(2)
	both := make(chan struct{})
	go func() {
		started.Wait()
		close(both)
	}()
	d.onRead = func(string) {
		started.Done()
		select {
		case <-both:
		case <-time.After(5 * time.Second):
			t.Error("the sides were copied one after the other")
		}
	}
	out, err := svc.VideoDiff(ctx, "/work/game", &ImageRef{Source: "snapshot", Path: "a.mp4"}, &ImageRef{Source: "revision", Revision: "HEAD", Path: "b.mp4"})
	require.NoError(t, err)
	require.Equal(t, "old video", served(t, svc, out.Old.URL))
	require.Equal(t, "new video", served(t, svc, out.New.URL))
}

func TestService_VideoDiffCleansUpWhenOneSideFails(t *testing.T) {
	svc, d := newModels(t, map[string]string{"a.mp4": "old video"})
	d.failures["b.mp4"] = &daemonpb.OpFailure{Code: 500, Message: "boom"}
	_, err := svc.VideoDiff(ctx, "/work/game", &ImageRef{Source: "snapshot", Path: "a.mp4"}, &ImageRef{Source: "snapshot", Path: "b.mp4"})
	require.EqualError(t, err, "boom")
	require.Empty(t, svc.media.files)
	temps, err := os.ReadDir(svc.media.dir)
	require.NoError(t, err)
	require.Empty(t, temps, "the side that finished is removed too")

	require.Equal(t, context.Canceled, firstVideoError([2]error{context.Canceled, nil}))
	require.NoError(t, firstVideoError([2]error{}))
}

func TestService_ModelDiffServesWorkingFilesInPlace(t *testing.T) {
	files := map[string]string{
		"art/hero.gltf":    heroGLTF,
		"art/hero.bin":     "BIN!",
		"art/tex/skin.png": "PNG",
		"art/ship.obj":     "mtllib ship.mtl\nv 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\n",
		"art/ship.mtl":     "newmtl Hull\nmap_Kd hull.png\n",
		"art/hull.png":     "HULL",
	}
	root := workTree(t, files)
	svc, d := newModels(t, files)
	out, err := svc.ModelDiff(ctx, root, nil, &ImageRef{Source: "working", Path: "art/hero.gltf"})
	require.NoError(t, err)
	n := out.New
	require.Equal(t, heroGLTF, served(t, svc, n.URL))
	require.Equal(t, map[string]string{"hero.bin": "BIN!", "tex/skin.png": "PNG"}, servedFiles(t, svc, n.Files))
	var read []string
	for _, r := range d.reads {
		read = append(read, r.GetBlob().GetPath())
	}
	// The model is read for its stats; a texture missing from disk is asked
	// of the daemon, which reports it missing. Present textures are not read.
	require.Equal(t, []string{"art/hero.gltf", "art/gone.png"}, read)
	require.Equal(t, []string{"../../outside.png", "gone.png"}, n.Missing)
	for _, token := range n.Tokens {
		f, ok := svc.media.lookup(token, false)
		require.True(t, ok)
		require.False(t, f.owned, "working files are registered, not copied")
	}
	require.Empty(t, svc.media.dir)

	out, err = svc.ModelDiff(ctx, root, nil, &ImageRef{Source: "working", Path: "art/ship.obj"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"ship.mtl": files["art/ship.mtl"], "hull.png": "HULL"}, servedFiles(t, svc, out.New.Files))

	// Textures over the render budget are caught from their size alone.
	big := filepath.Join(root, "art", "hero.bin")
	require.NoError(t, os.Truncate(big, maxRenderBytes))
	out, err = svc.ModelDiff(ctx, root, nil, &ImageRef{Source: "working", Path: "art/hero.gltf"})
	require.NoError(t, err)
	require.True(t, out.New.TooLarge)
	require.Empty(t, out.New.URL)
}

func TestService_ModelAndAudioDropFilesOnFailure(t *testing.T) {
	svc, d := newModels(t, map[string]string{"a.gltf": heroGLTF, "hero.bin": "B", "tex/skin.png": "P", "hit.wav": toneWAV(8000, 0.1, 0.5)})
	d.failures["b.gltf"] = &daemonpb.OpFailure{Code: 500, Message: "boom"}
	_, err := svc.ModelDiff(ctx, "/w", &ImageRef{Source: "snapshot", Path: "a.gltf"}, &ImageRef{Source: "snapshot", Path: "b.gltf"})
	require.EqualError(t, err, "boom")
	_, err = svc.AudioDiff(ctx, "/w", &ImageRef{Source: "snapshot", Path: "hit.wav"}, &ImageRef{Source: "snapshot", Path: "b.gltf"})
	require.EqualError(t, err, "boom")
	require.Empty(t, svc.media.files, "the side that loaded is dropped with the failed call")

	svc.media.dir = filepath.Join(t.TempDir(), "gone")
	_, err = svc.ModelDiff(ctx, "/w", nil, &ImageRef{Source: "snapshot", Path: "a.gltf"})
	require.Error(t, err)
	_, err = svc.AudioDiff(ctx, "/w", nil, &ImageRef{Source: "snapshot", Path: "hit.wav"})
	require.Error(t, err)
	require.Empty(t, svc.media.files)
	require.False(t, strings.Contains(svc.media.dir, "nipa-media"))

	// A working model is registered, then copying a texture the clone does
	// not hold fails: the model's token goes too.
	root := workTree(t, map[string]string{"a.gltf": heroGLTF})
	_, err = svc.ModelDiff(ctx, root, nil, &ImageRef{Source: "working", Path: "a.gltf"})
	require.Error(t, err)
	require.Empty(t, svc.media.files)
}
