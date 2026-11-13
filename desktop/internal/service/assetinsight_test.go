package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/provenance"
)

type insightDaemon struct {
	*opsDaemon
	refsReq *daemonpb.AssetRefsRequest
	refs    *daemonpb.AssetRefsResponse
	provReq *daemonpb.ProvenanceRequest
	prov    *daemonpb.ProvenanceResponse
	setReq  *daemonpb.SetProvenanceRequest
	set     *daemonpb.SetProvenanceResponse
	err     error
}

func (d *insightDaemon) AssetRefs(_ context.Context, req *daemonpb.AssetRefsRequest, _ ...grpc.CallOption) (*daemonpb.AssetRefsResponse, error) {
	d.refsReq = req
	return d.refs, d.err
}

func (d *insightDaemon) Provenance(_ context.Context, req *daemonpb.ProvenanceRequest, _ ...grpc.CallOption) (*daemonpb.ProvenanceResponse, error) {
	d.provReq = req
	return d.prov, d.err
}

func (d *insightDaemon) SetProvenance(_ context.Context, req *daemonpb.SetProvenanceRequest, _ ...grpc.CallOption) (*daemonpb.SetProvenanceResponse, error) {
	d.setReq = req
	return d.set, d.err
}

func newInsight(t *testing.T) (*Service, *insightDaemon) {
	t.Helper()
	d := &insightDaemon{opsDaemon: &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

func TestService_AssetRefs(t *testing.T) {
	svc, d := newInsight(t)
	d.refs = &daemonpb.AssetRefsResponse{
		Path: "Assets/hero.png", Asset: true, Scanned: 40, Truncated: true, Partial: true,
		Uses:    []*daemonpb.AssetRef{{Path: "Assets/a.mat", Raw: "guid:a", Resolved: true}, {Raw: "guid:b"}},
		UsedBy:  []string{"Assets/Hero.prefab"},
		Unknown: []*daemonpb.AssetRef{{Path: "Other/x.png", Raw: "res://Other/x.png"}},
	}
	out, err := svc.AssetRefs(ctx, "/work/game", "Assets/hero.png")
	require.NoError(t, err)
	assert.Equal(t, "Assets/hero.png", d.refsReq.GetPath())
	assert.Equal(t, AssetReferences{
		Path: "Assets/hero.png", Asset: true, Scanned: 40, Truncated: true, Partial: true,
		Uses:    []AssetRef{{Path: "Assets/a.mat", Raw: "guid:a", Resolved: true}, {Raw: "guid:b"}},
		UsedBy:  []string{"Assets/Hero.prefab"},
		Unknown: []AssetRef{{Path: "Other/x.png", Raw: "res://Other/x.png"}},
	}, out)

	d.refs = &daemonpb.AssetRefsResponse{Path: "README.md"}
	out, err = svc.AssetRefs(ctx, "/work/game", "README.md")
	require.NoError(t, err)
	assert.Equal(t, AssetReferences{Path: "README.md", Uses: []AssetRef{}, UsedBy: []string{}, Unknown: []AssetRef{}}, out)

	d.err = status.Error(codes.FailedPrecondition, "asset references are not configured")
	_, err = svc.AssetRefs(ctx, "/work/game", "a.tscn")
	assert.EqualError(t, err, "asset references are not configured")
}

func TestService_Provenance(t *testing.T) {
	svc, d := newInsight(t)
	seed := uint64(18446744073709551615)
	d.prov = &daemonpb.ProvenanceResponse{Enabled: true, Entries: []*daemonpb.ProvenanceEntry{
		{Path: "a.png", Status: "edited", Required: true, HasRecord: true, Exists: true, Tool: "ComfyUI", Model: "sdxl", Seed: &seed, Source: "png-text",
			RecordedAt: "2026-01-02T03:04:05Z", License: "cc0", HumanEdited: true, PromptSha256: "ab", DigitalSourceType: "trainedAlgorithmicMedia", Note: "n"},
		{Path: "b.png", Status: "missing", Required: true, Exists: true},
	}}
	out, err := svc.Provenance(ctx, "/work/game", []string{"a.png", "b.png"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a.png", "b.png"}, d.provReq.GetPaths())
	assert.Equal(t, Provenance{Enabled: true, Origins: []Origin{
		{Path: "a.png", Status: "edited", Required: true, HasRecord: true, Exists: true, Tool: "ComfyUI", Model: "sdxl", Seed: "18446744073709551615", Source: "png-text",
			RecordedAt: "2026-01-02T03:04:05Z", License: "cc0", HumanEdited: true, PromptHash: "ab", DigitalSourceType: "trainedAlgorithmicMedia", Note: "n"},
		{Path: "b.png", Status: "missing", Required: true, Exists: true},
	}}, out)

	d.prov = &daemonpb.ProvenanceResponse{}
	out, err = svc.Provenance(ctx, "/work/game", []string{"c.png"})
	require.NoError(t, err)
	assert.Equal(t, Provenance{Origins: []Origin{}}, out)

	d.err = errors.New("boom")
	_, err = svc.Provenance(ctx, "/work/game", []string{"c.png"})
	assert.Error(t, err)
}

func TestService_SetProvenance(t *testing.T) {
	svc, d := newInsight(t)
	d.set = &daemonpb.SetProvenanceResponse{Created: true, Entry: &daemonpb.ProvenanceEntry{Path: "a.png", Status: "recorded", HasRecord: true, Exists: true, Tool: "ComfyUI", Source: "manual"}}
	out, err := svc.SetProvenance(ctx, "/work/game", OriginInput{
		Path: "/a.png/", Tool: " ComfyUI ", Model: "sdxl", Seed: " 42 ", License: " ", Prompt: "  a dragon \n", Note: "pass 2", HumanEdited: true,
	})
	require.NoError(t, err)
	assert.True(t, out.Created)
	assert.Equal(t, "recorded", out.Origin.Status)
	r := d.setReq
	assert.Equal(t, "a.png", r.GetPath())
	assert.Equal(t, "ComfyUI", r.GetTool())
	assert.Equal(t, "sdxl", r.GetModel())
	assert.Equal(t, uint64(42), r.GetSeed())
	assert.Nil(t, r.License)
	assert.Equal(t, "pass 2", r.GetNote())
	assert.True(t, r.GetHumanEdited())
	assert.Equal(t, provenance.PromptHash("a dragon"), r.GetPromptSha256())

	d.setReq = nil
	out, err = svc.SetProvenance(ctx, "/work/game", OriginInput{Path: "a.png", Tool: "Firefly"})
	require.NoError(t, err)
	assert.Nil(t, d.setReq.Model)
	assert.Nil(t, d.setReq.Seed)
	assert.Nil(t, d.setReq.PromptSha256)
	assert.Nil(t, d.setReq.Note)
	assert.Equal(t, "a.png", out.Origin.Path)
}

func TestService_SetProvenanceValidation(t *testing.T) {
	svc, d := newInsight(t)
	for _, tc := range []struct {
		in   OriginInput
		want string
	}{
		{OriginInput{Tool: "x"}, "choose a file"},
		{OriginInput{Path: "a.png", Tool: "  "}, "tool is required"},
		{OriginInput{Path: "a.png", Tool: "x", Seed: "-1"}, "seed must be a whole number"},
		{OriginInput{Path: "a.png", Tool: "x", Seed: "1.5"}, "seed must be a whole number"},
	} {
		_, err := svc.SetProvenance(ctx, "/work/game", tc.in)
		assert.EqualError(t, err, tc.want)
	}
	assert.Nil(t, d.setReq)

	d.err = status.Error(codes.InvalidArgument, "cannot read a.png")
	_, err := svc.SetProvenance(ctx, "/work/game", OriginInput{Path: "a.png", Tool: "x"})
	assert.EqualError(t, err, "cannot read a.png")
}
