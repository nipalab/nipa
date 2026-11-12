package service

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/provenance"
)

type AssetRef struct {
	Path     string `json:"path"`
	Raw      string `json:"raw"`
	Resolved bool   `json:"resolved"`
}

// AssetReferences lists what an engine asset uses and which files use it.
// Asset is false for files that cannot take part in a reference.
type AssetReferences struct {
	Path      string     `json:"path"`
	Asset     bool       `json:"asset"`
	Uses      []AssetRef `json:"uses"`
	UsedBy    []string   `json:"usedBy"`
	Unknown   []AssetRef `json:"unknown"`
	Scanned   int        `json:"scanned"`
	Truncated bool       `json:"truncated"`
	Partial   bool       `json:"partial"`
}

// Origin is the provenance of one file. Status is recorded, modified, edited
// or missing; Seed is a decimal string so 64-bit seeds survive JavaScript.
type Origin struct {
	Path              string `json:"path"`
	Status            string `json:"status"`
	Required          bool   `json:"required"`
	HasRecord         bool   `json:"hasRecord"`
	Exists            bool   `json:"exists"`
	Tool              string `json:"tool"`
	Model             string `json:"model"`
	Seed              string `json:"seed"`
	Source            string `json:"source"`
	RecordedAt        string `json:"recordedAt"`
	License           string `json:"license"`
	HumanEdited       bool   `json:"humanEdited"`
	PromptHash        string `json:"promptHash"`
	DigitalSourceType string `json:"digitalSourceType"`
	Note              string `json:"note"`
}

// Provenance reports whether the clone tracks provenance and the origin of
// each asked path that has a record or that a rule requires one for.
type Provenance struct {
	Enabled bool     `json:"enabled"`
	Origins []Origin `json:"origins"`
}

// OriginInput is the "Add record" form. The prompt is hashed here and never
// leaves the app; empty fields are left unchanged.
type OriginInput struct {
	Path        string `json:"path"`
	Tool        string `json:"tool"`
	Model       string `json:"model"`
	Seed        string `json:"seed"`
	License     string `json:"license"`
	Prompt      string `json:"prompt"`
	Note        string `json:"note"`
	HumanEdited bool   `json:"humanEdited"`
}

type OriginSaved struct {
	Origin  Origin `json:"origin"`
	Created bool   `json:"created"`
}

// AssetRefs reports the references of one file from the daemon's cached
// reference graph.
func (s *Service) AssetRefs(ctx context.Context, root, path string) (AssetReferences, error) {
	out := AssetReferences{Path: path, Uses: []AssetRef{}, UsedBy: []string{}, Unknown: []AssetRef{}}
	var res *daemonpb.AssetRefsResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.AssetRefs(ctx, &daemonpb.AssetRefsRequest{Root: root, Path: path})
		return err
	})
	if err != nil {
		return out, err
	}
	out.Path, out.Asset = res.GetPath(), res.GetAsset()
	out.Scanned, out.Truncated, out.Partial = int(res.GetScanned()), res.GetTruncated(), res.GetPartial()
	for _, r := range res.GetUses() {
		out.Uses = append(out.Uses, AssetRef{Path: r.GetPath(), Raw: r.GetRaw(), Resolved: r.GetResolved()})
	}
	for _, r := range res.GetUnknown() {
		out.Unknown = append(out.Unknown, AssetRef{Path: r.GetPath(), Raw: r.GetRaw(), Resolved: r.GetResolved()})
	}
	out.UsedBy = append(out.UsedBy, res.GetUsedBy()...)
	return out, nil
}

// Provenance reports the origin of the given files.
func (s *Service) Provenance(ctx context.Context, root string, paths []string) (Provenance, error) {
	out := Provenance{Origins: []Origin{}}
	var res *daemonpb.ProvenanceResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.Provenance(ctx, &daemonpb.ProvenanceRequest{Root: root, Paths: paths})
		return err
	})
	if err != nil {
		return out, err
	}
	out.Enabled = res.GetEnabled()
	for _, e := range res.GetEntries() {
		out.Origins = append(out.Origins, toOrigin(e))
	}
	return out, nil
}

// SetProvenance records a file's origin by hand and stages
// .nipaprovenance.yml, creating it when the clone has none yet.
func (s *Service) SetProvenance(ctx context.Context, root string, in OriginInput) (OriginSaved, error) {
	req, err := originRequest(root, in)
	if err != nil {
		return OriginSaved{}, err
	}
	var res *daemonpb.SetProvenanceResponse
	err = s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.SetProvenance(ctx, req)
		return err
	})
	if err != nil {
		return OriginSaved{}, err
	}
	return OriginSaved{Origin: toOrigin(res.GetEntry()), Created: res.GetCreated()}, nil
}

func originRequest(root string, in OriginInput) (*daemonpb.SetProvenanceRequest, error) {
	path := strings.Trim(strings.TrimSpace(in.Path), "/")
	if path == "" {
		return nil, errors.New("choose a file")
	}
	tool := strings.TrimSpace(in.Tool)
	if tool == "" {
		return nil, errors.New("tool is required")
	}
	req := &daemonpb.SetProvenanceRequest{Root: root, Path: path, Tool: &tool, HumanEdited: &in.HumanEdited}
	optional := func(v string) *string {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		return &v
	}
	req.Model, req.License = optional(in.Model), optional(in.License)
	if note := strings.TrimSpace(in.Note); note != "" {
		req.Note = &note
	}
	if seed := strings.TrimSpace(in.Seed); seed != "" {
		n, err := strconv.ParseUint(seed, 10, 64)
		if err != nil {
			return nil, errors.New("seed must be a whole number")
		}
		req.Seed = &n
	}
	if prompt := strings.TrimSpace(in.Prompt); prompt != "" {
		hash := provenance.PromptHash(prompt)
		req.PromptSha256 = &hash
	}
	return req, nil
}

func toOrigin(e *daemonpb.ProvenanceEntry) Origin {
	o := Origin{
		Path: e.GetPath(), Status: e.GetStatus(), Required: e.GetRequired(), HasRecord: e.GetHasRecord(), Exists: e.GetExists(),
		Tool: e.GetTool(), Model: e.GetModel(), Source: e.GetSource(), RecordedAt: e.GetRecordedAt(), License: e.GetLicense(),
		HumanEdited: e.GetHumanEdited(), PromptHash: e.GetPromptSha256(), DigitalSourceType: e.GetDigitalSourceType(), Note: e.GetNote(),
	}
	if e.Seed != nil {
		o.Seed = strconv.FormatUint(e.GetSeed(), 10)
	}
	return o
}
