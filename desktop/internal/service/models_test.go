package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type blobDaemon struct {
	*opsDaemon
	files    map[string]string
	failures map[string]*daemonpb.OpFailure
	sizes    map[string]int64
	mu       sync.Mutex
	reads    []*daemonpb.ReadBlobRequest
	err      error
	// onRead runs when a read starts, onDrained when its stream ends.
	onRead    func(path string)
	onDrained func(path string)
}

type drainedStream struct {
	*listStream[daemonpb.BlobEvent]
	done func()
}

func (s *drainedStream) Recv() (*daemonpb.BlobEvent, error) {
	ev, err := s.listStream.Recv()
	if err != nil && s.done != nil {
		s.done()
	}
	return ev, err
}

func (d *blobDaemon) ReadBlob(_ context.Context, req *daemonpb.ReadBlobRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.BlobEvent], error) {
	d.mu.Lock()
	d.reads = append(d.reads, req)
	d.mu.Unlock()
	if d.onRead != nil {
		d.onRead(req.GetBlob().GetPath())
	}
	if d.err != nil {
		return nil, d.err
	}
	p := req.GetBlob().GetPath()
	if f := d.failures[p]; f != nil {
		return &listStream[daemonpb.BlobEvent]{items: []*daemonpb.BlobEvent{{Event: &daemonpb.BlobEvent_Failure{Failure: f}}}}, nil
	}
	content, ok := d.files[p]
	if !ok {
		return &listStream[daemonpb.BlobEvent]{items: []*daemonpb.BlobEvent{{Event: &daemonpb.BlobEvent_Failure{Failure: &daemonpb.OpFailure{Code: 404, Message: "missing"}}}}}, nil
	}
	size := int64(len(content))
	if s, ok := d.sizes[p]; ok {
		size = s
	}
	evs := []*daemonpb.BlobEvent{{Event: &daemonpb.BlobEvent_Info{Info: &daemonpb.BlobInfo{Path: p, SizeBytes: size}}}}
	for len(content) > 0 {
		n := min(len(content), 4)
		evs = append(evs, &daemonpb.BlobEvent{Event: &daemonpb.BlobEvent_Data{Data: []byte(content[:n])}})
		content = content[n:]
	}
	stream := &listStream[daemonpb.BlobEvent]{items: evs}
	if d.onDrained != nil {
		return &drainedStream{listStream: stream, done: func() { d.onDrained(p) }}, nil
	}
	return stream, nil
}

func newModels(t *testing.T, files map[string]string) (*Service, *blobDaemon) {
	t.Helper()
	d := &blobDaemon{opsDaemon: &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}, files: files, failures: map[string]*daemonpb.OpFailure{}, sizes: map[string]int64{}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

const heroGLTF = `{"asset":{"version":"2.0","generator":"test"},"scenes":[{"nodes":[0]}],"nodes":[{"mesh":0}],
"meshes":[{"primitives":[{"attributes":{"POSITION":0}}]}],"accessors":[{"count":3,"min":[0,0,0],"max":[1,2,3]}],
"materials":[{"name":"Skin"}],"buffers":[{"uri":"hero.bin"}],"images":[{"uri":"tex/skin.png"},{"uri":"../../outside.png"},{"uri":"gone.png"}]}`

// served fetches a media URL through MediaMiddleware the way the webview
// does and returns the body.
func served(t *testing.T, svc *Service, url string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	MediaMiddleware(svc)(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	require.Equal(t, http.StatusOK, rec.Code, url)
	return rec.Body.String()
}

func servedFiles(t *testing.T, svc *Service, files map[string]string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name, url := range files {
		out[name] = served(t, svc, url)
	}
	return out
}

func TestService_ModelDiffLoadsFilesAndStats(t *testing.T) {
	svc, d := newModels(t, map[string]string{
		"art/hero.gltf":        heroGLTF,
		"art/hero.bin":         "BIN!",
		"art/tex/skin.png":     "PNG",
		"art/ship.obj":         "mtllib ship.mtl\nv 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\n",
		"art/ship.mtl":         "newmtl Hull\nmap_Kd paint/hull.png\n",
		"art/paint/hull.png":   "HULL",
		"art/old/hero.gltf":    heroGLTF,
		"art/old/hero.bin":     "OLD",
		"art/old/tex/skin.png": "PNG0",
	})
	out, err := svc.ModelDiff(ctx, "/work/game", &ImageRef{Source: "revision", Revision: "HEAD", Path: "art/old/hero.gltf"}, &ImageRef{Source: "working", Path: "art/hero.gltf"})
	require.NoError(t, err)
	n := out.New
	require.Equal(t, "gltf", n.Format)
	require.Equal(t, heroGLTF, served(t, svc, n.URL))
	require.True(t, strings.HasPrefix(n.URL, MediaPrefix+n.Media+"/hero.gltf"))
	require.Equal(t, map[string]string{"hero.bin": "BIN!", "tex/skin.png": "PNG"}, servedFiles(t, svc, n.Files))
	require.Len(t, n.Tokens, 3)
	require.Equal(t, n.Media, n.Tokens[0])
	require.Equal(t, []string{"../../outside.png", "gone.png"}, n.Missing)
	require.Equal(t, &ModelStats{Generator: "test", Meshes: 1, Primitives: 1, Vertices: 3, Triangles: 1, Materials: []string{"Skin"}, Nodes: 1, Animations: []string{}, Min: []float64{0, 0, 0}, Max: []float64{1, 2, 3}}, n.Stats)
	require.Equal(t, "OLD", served(t, svc, out.Old.Files["hero.bin"]))
	for _, r := range d.reads {
		if strings.HasPrefix(r.GetBlob().GetPath(), "art/old/") {
			require.Equal(t, "HEAD", r.GetBlob().GetRevision())
		}
	}

	out, err = svc.ModelDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "art/ship.obj"})
	require.NoError(t, err)
	require.Nil(t, out.Old)
	require.Equal(t, map[string]string{"ship.mtl": "newmtl Hull\nmap_Kd paint/hull.png\n", "paint/hull.png": "HULL"}, servedFiles(t, svc, out.New.Files))
	require.Equal(t, 1, out.New.Stats.Triangles)
	require.Empty(t, out.New.Stats.Animations)
}

func TestService_ModelDiffWithoutStatsOrBudget(t *testing.T) {
	svc, d := newModels(t, map[string]string{
		"a.fbx":   "Kaydara",
		"big.glb": "x",
		"b.gltf":  `{"asset":{"version":"2.0"},"buffers":[{"uri":"b.bin"},{"uri":"c.bin"}]}`,
		"b.bin":   "x",
		"c.bin":   "y",
		"bad.obj": "v 1\n",
	})
	out, err := svc.ModelDiff(ctx, "/w", nil, &ImageRef{Path: "a.fbx"})
	require.NoError(t, err)
	require.Nil(t, out.New.Stats)
	require.Equal(t, "unsupported format: fbx model statistics", out.New.StatsError)
	require.Equal(t, "Kaydara", served(t, svc, out.New.URL))

	out, err = svc.ModelDiff(ctx, "/w", nil, &ImageRef{Path: "bad.obj"})
	require.NoError(t, err)
	require.Contains(t, out.New.StatsError, "invalid model")

	d.sizes["big.glb"] = maxModelBytes + 1
	out, err = svc.ModelDiff(ctx, "/w", &ImageRef{Path: "big.glb"}, nil)
	require.NoError(t, err)
	require.True(t, out.Old.TooLarge)
	require.Equal(t, int64(maxModelBytes+1), out.Old.Size)
	require.Empty(t, out.Old.URL)
	require.Empty(t, out.Old.Tokens)

	d.sizes["b.bin"] = maxRenderBytes
	out, err = svc.ModelDiff(ctx, "/w", nil, &ImageRef{Path: "b.gltf"})
	require.NoError(t, err)
	require.True(t, out.New.TooLarge)
	require.Empty(t, out.New.URL)
	require.Empty(t, out.New.Files)
	require.NotNil(t, out.New.Stats)

	d.sizes["b.bin"] = 1
	d.files["b.bin"] = strings.Repeat("z", 9)
	out, err = svc.ModelDiff(ctx, "/w", nil, &ImageRef{Path: "b.gltf"})
	require.NoError(t, err)
	require.Contains(t, out.New.Files, "b.bin")
}

func TestService_ModelDiffFailures(t *testing.T) {
	svc, d := newModels(t, map[string]string{"a.gltf": `{"asset":{"version":"2.0"},"buffers":[{"uri":"x.bin"}]}`})
	_, err := svc.ModelDiff(ctx, "/w", nil, nil)
	require.EqualError(t, err, "nothing to compare")
	_, err = svc.ModelDiff(ctx, "/w", nil, &ImageRef{Path: "nope.glb"})
	require.ErrorIs(t, err, errBlobMissing)

	d.failures["x.bin"] = &daemonpb.OpFailure{Code: 500, Message: "disk error"}
	_, err = svc.ModelDiff(ctx, "/w", nil, &ImageRef{Path: "a.gltf"})
	require.EqualError(t, err, "disk error")

	d.err = status.Error(codes.Internal, "no stream")
	_, err = svc.ModelDiff(ctx, "/w", &ImageRef{Path: "a.gltf"}, nil)
	require.EqualError(t, err, "no stream")

	d.watchErr["/lost"] = status.Error(codes.InvalidArgument, "not a clone")
	_, err = svc.ModelDiff(ctx, "/lost", nil, &ImageRef{Path: "a.gltf"})
	require.EqualError(t, err, "not a clone")

	e := &erroringOps{opsDaemon: d.opsDaemon, err: status.Error(codes.Unavailable, "daemon gone")}
	_, _, err = svc.readBlob(ctx, e, "/w", &daemonpb.BlobRef{Path: "a"}, 10)
	require.EqualError(t, err, "daemon gone")
}

func TestService_ModelDiffReportsFilesBeyondCap(t *testing.T) {
	files := map[string]string{}
	var lines []string
	for i := range maxModelFiles + 3 {
		name := fmt.Sprintf("m%02d.mtl", i)
		lines = append(lines, "mtllib "+name)
		if i != 1 {
			files["art/"+name] = "newmtl X\n"
		}
		if i == 0 || i == 2 {
			files["art/"+name] = "newmtl X\nmap_Kd skin.png\n"
		}
	}
	files["art/ship.obj"] = strings.Join(lines, "\n") + "\nv 0 0 0\n"
	svc, d := newModels(t, files)
	out, err := svc.ModelDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "art/ship.obj"})
	require.NoError(t, err)
	n := out.New
	require.False(t, n.TooLarge)
	require.Len(t, n.Files, maxModelFiles-1)
	require.Equal(t, []string{"m01.mtl", "m64.mtl", "m65.mtl", "m66.mtl", "skin.png"}, n.Missing)
	require.Len(t, d.reads, maxModelFiles+1)
}

func TestService_ModelDiffCapsMissingList(t *testing.T) {
	files := map[string]string{}
	var lines []string
	for i := range maxModelFiles * 4 {
		name := fmt.Sprintf("m%03d.mtl", i)
		lines = append(lines, "mtllib "+name)
		if i < maxModelFiles-1 {
			files["art/"+name] = "newmtl X\n"
		}
	}
	files["art/ship.obj"] = strings.Join(lines, "\n") + "\n"
	svc, _ := newModels(t, files)
	out, err := svc.ModelDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "art/ship.obj"})
	require.NoError(t, err)
	require.Len(t, out.New.Files, maxModelFiles-1)
	require.Len(t, out.New.Missing, maxModelFiles)
	require.Equal(t, fmt.Sprintf("m%03d.mtl", 2*maxModelFiles-2), out.New.Missing[maxModelFiles-1])
}

func TestResolveModelPath(t *testing.T) {
	for dep, want := range map[string]string{"a.bin": "art/a.bin", "../shared/t.png": "shared/t.png", "x/../y.png": "art/y.png"} {
		got, ok := resolveModelPath("art/m.gltf", dep)
		require.True(t, ok, dep)
		require.Equal(t, want, got)
	}
	for _, dep := range []string{"", "/etc/passwd", "../../x", "..\\x", ".."} {
		_, ok := resolveModelPath("art/m.gltf", dep)
		require.False(t, ok, dep)
	}
	_, ok := resolveModelPath("m.gltf", "../x")
	require.False(t, ok)
}
