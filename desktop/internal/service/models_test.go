package service

import (
	"context"
	"encoding/base64"
	"strings"
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
	reads    []*daemonpb.ReadBlobRequest
	err      error
}

func (d *blobDaemon) ReadBlob(_ context.Context, req *daemonpb.ReadBlobRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.BlobEvent], error) {
	d.reads = append(d.reads, req)
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
	return &listStream[daemonpb.BlobEvent]{items: evs}, nil
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

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

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
	require.Equal(t, b64(heroGLTF), n.Data)
	require.Equal(t, map[string]string{"hero.bin": b64("BIN!"), "tex/skin.png": b64("PNG")}, n.Files)
	require.Equal(t, []string{"../../outside.png", "gone.png"}, n.Missing)
	require.Equal(t, &ModelStats{Generator: "test", Meshes: 1, Primitives: 1, Vertices: 3, Triangles: 1, Materials: []string{"Skin"}, Nodes: 1, Animations: []string{}, Min: []float64{0, 0, 0}, Max: []float64{1, 2, 3}}, n.Stats)
	require.Equal(t, b64("OLD"), out.Old.Files["hero.bin"])
	for _, r := range d.reads {
		if strings.HasPrefix(r.GetBlob().GetPath(), "art/old/") {
			require.Equal(t, "HEAD", r.GetBlob().GetRevision())
		}
	}

	out, err = svc.ModelDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "art/ship.obj"})
	require.NoError(t, err)
	require.Nil(t, out.Old)
	require.Equal(t, map[string]string{"ship.mtl": b64("newmtl Hull\nmap_Kd paint/hull.png\n"), "paint/hull.png": b64("HULL")}, out.New.Files)
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
	require.Equal(t, b64("Kaydara"), out.New.Data)

	out, err = svc.ModelDiff(ctx, "/w", nil, &ImageRef{Path: "bad.obj"})
	require.NoError(t, err)
	require.Contains(t, out.New.StatsError, "invalid model")

	d.sizes["big.glb"] = maxModelBytes + 1
	out, err = svc.ModelDiff(ctx, "/w", &ImageRef{Path: "big.glb"}, nil)
	require.NoError(t, err)
	require.True(t, out.Old.TooLarge)
	require.Equal(t, int64(maxModelBytes+1), out.Old.Size)
	require.Empty(t, out.Old.Data)

	d.sizes["b.bin"] = maxRenderBytes
	out, err = svc.ModelDiff(ctx, "/w", nil, &ImageRef{Path: "b.gltf"})
	require.NoError(t, err)
	require.True(t, out.New.TooLarge)
	require.Empty(t, out.New.Data)
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
