package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/nipalab/nipa/internal/assetbudget"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/provenance"
)

const maxProvenancePaths = 1000

// Provenance reports the provenance record and status of each path that has a
// record or that a require_provenance rule covers; other paths are left out.
func (s *Server) Provenance(ctx context.Context, req *daemonpb.ProvenanceRequest) (*daemonpb.ProvenanceResponse, error) {
	if len(req.GetPaths()) > maxProvenancePaths {
		return nil, toStatusError(clientDomain.NewUserError(fmt.Sprintf("at most %d paths per call", maxProvenancePaths)))
	}
	var res *daemonpb.ProvenanceResponse
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		records, err := usecase.LoadProvenance(rp.root)
		if err != nil {
			return err
		}
		budget, err := assetbudget.Load(rp.root)
		if err != nil {
			return clientDomain.NewUserError(err.Error())
		}
		res = &daemonpb.ProvenanceResponse{Enabled: records != nil}
		for _, raw := range req.GetPaths() {
			if err := ctx.Err(); err != nil {
				return err
			}
			p, err := provenance.NormalizePath(raw)
			if err != nil {
				return clientDomain.NewUserError(err.Error())
			}
			var rec *provenance.Record
			if records != nil {
				rec = records.Get(p)
			}
			required := budget != nil && budget.RequiresProvenance(p)
			if rec == nil && !required {
				continue
			}
			entry, err := provenanceEntry(rp.root, p, rec, required)
			if err != nil {
				return err
			}
			res.Entries = append(res.Entries, entry)
		}
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return res, nil
}

func provenanceEntry(root, p string, rec *provenance.Record, required bool) (*daemonpb.ProvenanceEntry, error) {
	e := &daemonpb.ProvenanceEntry{Path: p, Required: required}
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
	switch {
	case err == nil:
		e.Exists = true
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	hash := ""
	if e.Exists && rec != nil && rec.ContentHash != "" {
		if hash, err = usecase.HashWorkingFile(root, p); err != nil {
			return nil, err
		}
	}
	e.Status = provenance.Status(rec, hash)
	fillRecord(e, rec)
	return e, nil
}

func fillRecord(e *daemonpb.ProvenanceEntry, rec *provenance.Record) {
	if rec == nil {
		return
	}
	e.HasRecord = true
	e.Tool, e.Model, e.Source, e.License = rec.Tool, rec.Model, rec.Source, rec.License
	e.HumanEdited, e.PromptSha256, e.DigitalSourceType, e.Note = rec.HumanEdited, rec.PromptSHA256, rec.DigitalSourceType, rec.Note
	if rec.Seed != nil {
		seed := *rec.Seed
		e.Seed = &seed
	}
	if !rec.RecordedAt.IsZero() {
		e.RecordedAt = rec.RecordedAt.UTC().Format(time.RFC3339)
	}
}

// SetProvenance records the provenance of one file by hand, like nipa
// provenance set: it writes .nipaprovenance.yml (creating it when missing)
// and stages it, under the shared slot like Stage.
func (s *Server) SetProvenance(ctx context.Context, req *daemonpb.SetProvenanceRequest) (*daemonpb.SetProvenanceResponse, error) {
	in := usecase.ProvenanceInput{
		Tool: req.Tool, Model: req.Model, Seed: req.Seed, License: req.License,
		HumanEdited: req.HumanEdited, Note: req.Note, PromptSHA256: req.PromptSha256,
	}
	var res *daemonpb.SetProvenanceResponse
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		defer rp.changes.publish([]string{provenance.FileName})
		out, err := rp.wc.SetProvenance(ctx, req.GetPath(), in)
		if err != nil {
			return err
		}
		required := false
		if budget, err := assetbudget.Load(rp.root); err == nil && budget != nil {
			required = budget.RequiresProvenance(out.Path)
		}
		entry := &daemonpb.ProvenanceEntry{Path: out.Path, Required: required, Exists: true, Status: provenance.StatusRecorded}
		fillRecord(entry, out.Record)
		res = &daemonpb.SetProvenanceResponse{Entry: entry, Created: out.Created}
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return res, nil
}
