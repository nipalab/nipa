package daemon

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/assetrefs"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type countingGraph struct {
	builds atomic.Int32
	inner  GraphBuilder
	graph  *assetrefs.Graph
	err    error
	gate   chan struct{}
}

func (c *countingGraph) Graph(ctx context.Context, root string) (*assetrefs.Graph, error) {
	c.builds.Add(1)
	if c.gate != nil {
		<-c.gate
	}
	if c.err != nil {
		return nil, c.err
	}
	if c.inner != nil {
		return c.inner.Graph(ctx, root)
	}
	return c.graph, nil
}

const godotScene = `[gd_scene load_steps=3 format=3]

[ext_resource type="Texture2D" path="res://art/hero.png" id="1_a"]
[ext_resource type="Texture2D" path="res://art/gone.png" id="2_b"]

[node name="Main" type="Node2D"]
`

func godotFiles() map[string]string {
	return map[string]string{
		"project.godot":    "[application]\n",
		"scenes/main.tscn": godotScene,
		"art/hero.png":     "png",
		"art/unused.png":   "png",
		"notes.txt":        "hello",
	}
}

func refsServer(t *testing.T, files map[string]string, builder GraphBuilder) (*Server, string) {
	t.Helper()
	root := newTreeClone(t, files)
	srv := newOpServer(t, RepoOps{Refs: builder})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root
}

func realGraph() GraphBuilder {
	return NewGraphBuilder(usecase.NewDiff(nil, nil, localrepo.NewLocalRepo()))
}

func TestServer_AssetRefs_Godot(t *testing.T) {
	srv, root := refsServer(t, godotFiles(), realGraph())
	ctx := context.Background()

	res, err := srv.AssetRefs(ctx, &daemonpb.AssetRefsRequest{Root: root, Path: "art/hero.png"})
	require.NoError(t, err)
	assert.True(t, res.GetAsset())
	assert.Equal(t, []string{"scenes/main.tscn"}, res.GetUsedBy())
	assert.False(t, res.GetPartial())
	assert.Positive(t, res.GetScanned())

	res, err = srv.AssetRefs(ctx, &daemonpb.AssetRefsRequest{Root: root, Path: "scenes/main.tscn"})
	require.NoError(t, err)
	require.Len(t, res.GetUses(), 2)
	byPath := map[string]bool{}
	for _, u := range res.GetUses() {
		byPath[u.GetPath()] = u.GetResolved()
	}
	assert.Equal(t, map[string]bool{"art/hero.png": true, "art/gone.png": false}, byPath)
	assert.Empty(t, res.GetUnknown())

	res, err = srv.AssetRefs(ctx, &daemonpb.AssetRefsRequest{Root: root, Path: "/art/unused.png"})
	require.NoError(t, err)
	assert.Equal(t, "art/unused.png", res.GetPath())
	assert.True(t, res.GetAsset())
	assert.Empty(t, res.GetUsedBy())
}

func TestServer_AssetRefs_SparseIsPartial(t *testing.T) {
	root := newTreeClone(t, godotFiles())
	lr := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, lr.SaveConfig(clientDomain.Config{Url: "https://nipa.example.com/default/default", Branch: "main", Sparse: []string{"scenes", "project.godot"}}))
	require.NoError(t, lr.Close())
	srv := newOpServer(t, RepoOps{Refs: realGraph()})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	res, err := srv.AssetRefs(context.Background(), &daemonpb.AssetRefsRequest{Root: root, Path: "scenes/main.tscn"})
	require.NoError(t, err)
	assert.True(t, res.GetPartial())
	require.Len(t, res.GetUnknown(), 1)
	assert.Equal(t, "art/gone.png", res.GetUnknown()[0].GetPath())
	require.Len(t, res.GetUses(), 1)
	assert.True(t, res.GetUses()[0].GetResolved())
}

func TestServer_AssetRefs_Truncated(t *testing.T) {
	stub := &countingGraph{graph: &assetrefs.Graph{Scanned: 5000, Truncated: true}}
	srv, root := refsServer(t, godotFiles(), stub)
	res, err := srv.AssetRefs(context.Background(), &daemonpb.AssetRefsRequest{Root: root, Path: "art/hero.png"})
	require.NoError(t, err)
	assert.True(t, res.GetTruncated())
	assert.Equal(t, int32(5000), res.GetScanned())
}

func TestServer_AssetRefs_CacheInvalidation(t *testing.T) {
	stub := &countingGraph{inner: realGraph()}
	srv, root := refsServer(t, godotFiles(), stub)
	ctx := context.Background()
	ask := func() {
		t.Helper()
		_, err := srv.AssetRefs(ctx, &daemonpb.AssetRefsRequest{Root: root, Path: "art/hero.png"})
		require.NoError(t, err)
	}
	rp, unref, err := srv.repos.ref(root)
	require.NoError(t, err)
	unref()

	ask()
	ask()
	assert.Equal(t, int32(1), stub.builds.Load())

	rp.changes.publish([]string{"notes.txt", ".nipaprovenance.yml"})
	ask()
	assert.Equal(t, int32(1), stub.builds.Load())

	rp.changes.publish([]string{"scenes/main.tscn"})
	ask()
	assert.Equal(t, int32(2), stub.builds.Load())

	rp.changes.publish([]string{"art/hero.png.import"})
	ask()
	assert.Equal(t, int32(3), stub.builds.Load())

	rp.changes.publish([]string{".nipaignore"})
	ask()
	assert.Equal(t, int32(4), stub.builds.Load())

	rp.changes.publish(nil)
	ask()
	assert.Equal(t, int32(5), stub.builds.Load())

	rp.graphMu.Lock()
	rp.graphAt = rp.graphAt.Add(-graphTTL)
	rp.graphMu.Unlock()
	ask()
	assert.Equal(t, int32(6), stub.builds.Load())
}

func TestServer_AssetRefs_ConcurrentBuildOnce(t *testing.T) {
	stub := &countingGraph{inner: realGraph(), gate: make(chan struct{})}
	srv, root := refsServer(t, godotFiles(), stub)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := srv.AssetRefs(ctx, &daemonpb.AssetRefsRequest{Root: root, Path: "art/hero.png"})
			errs <- err
		}()
	}
	close(stub.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), stub.builds.Load())
}

func TestServer_AssetRefs_NotAnAsset(t *testing.T) {
	stub := &countingGraph{inner: realGraph()}
	srv, root := refsServer(t, godotFiles(), stub)
	res, err := srv.AssetRefs(context.Background(), &daemonpb.AssetRefsRequest{Root: root, Path: "docs/readme.md"})
	require.NoError(t, err)
	assert.False(t, res.GetAsset())
	assert.Zero(t, stub.builds.Load())

	_, err = srv.AssetRefs(context.Background(), &daemonpb.AssetRefsRequest{Root: t.TempDir(), Path: "docs/readme.md"})
	require.Error(t, err)
}

func TestServer_AssetRefs_Errors(t *testing.T) {
	srv, root := refsServer(t, godotFiles(), nil)
	ctx := context.Background()

	_, err := srv.AssetRefs(ctx, &daemonpb.AssetRefsRequest{Root: root})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = srv.AssetRefs(ctx, &daemonpb.AssetRefsRequest{Root: root, Path: "../x.tscn"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = srv.AssetRefs(ctx, &daemonpb.AssetRefsRequest{Root: root, Path: "scenes/main.tscn"})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))

	failing, root2 := refsServer(t, godotFiles(), &countingGraph{err: errors.New("walk failed")})
	_, err = failing.AssetRefs(ctx, &daemonpb.AssetRefsRequest{Root: root2, Path: "scenes/main.tscn"})
	require.ErrorContains(t, err, "walk failed")
}
