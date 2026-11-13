package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/provenance"
)

// ProvenanceInput holds the fields of a hand-written provenance record; nil
// fields keep the existing value.
type ProvenanceInput struct {
	Tool         *string
	Model        *string
	Seed         *uint64
	License      *string
	HumanEdited  *bool
	Note         *string
	PromptSHA256 *string
}

// ProvenanceSet is the outcome of SetProvenance.
type ProvenanceSet struct {
	Path    string
	Record  *provenance.Record
	Created bool
}

// SetProvenance creates or updates the record of p as a manual record with
// the working file's content hash, writes .nipaprovenance.yml (creating it,
// which opts the repository in) and stages it.
func (w *WorkingCopy) SetProvenance(ctx context.Context, p string, in ProvenanceInput) (*ProvenanceSet, error) {
	n, err := provenance.NormalizePath(p)
	if err != nil {
		return nil, domain.NewUserError(err.Error())
	}
	if n == provenance.FileName {
		return nil, domain.NewUserError("cannot record the provenance of " + provenance.FileName)
	}
	hash, err := HashWorkingFile(w.root, n)
	if err != nil {
		return nil, domain.NewUserError(fmt.Sprintf("cannot read %s: %v", n, err))
	}
	f, err := LoadProvenance(w.root)
	if err != nil {
		return nil, err
	}
	created := f == nil
	if created {
		f = provenance.New()
	}
	rec := f.Get(n)
	if rec == nil {
		rec = &provenance.Record{}
	}
	trim := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	trim(&rec.Tool, in.Tool)
	trim(&rec.Model, in.Model)
	trim(&rec.License, in.License)
	trim(&rec.PromptSHA256, in.PromptSHA256)
	if in.Note != nil {
		rec.Note = *in.Note
	}
	if in.Seed != nil {
		seed := *in.Seed
		rec.Seed = &seed
	}
	if in.HumanEdited != nil {
		rec.HumanEdited = *in.HumanEdited
	}
	rec.Source, rec.C2PAVerified = provenance.SourceManual, nil
	rec.ContentHash = hash
	rec.RecordedAt = provenanceNow()
	if err := f.Set(n, rec); err != nil {
		return nil, domain.NewUserError(err.Error())
	}
	if err := SaveProvenance(w.root, f); err != nil {
		return nil, err
	}
	if err := w.Add(ctx, []string{provenance.FileName}, AddOptions{Force: true, NoProvenance: true}); err != nil {
		return nil, err
	}
	return &ProvenanceSet{Path: n, Record: rec, Created: created}, nil
}
