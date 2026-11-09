package assetscan

import (
	"context"
	"errors"
	"io/fs"
	"sort"

	"github.com/nipalab/nipa/internal/assetbudget"
	"github.com/nipalab/nipa/internal/assetdiff"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/provenance"
)

// ProvenanceRequest selects the files of a provenance report: the files
// changed since a revision (against the working copy), or every recorded
// file plus every tracked file a require_provenance rule covers.
type ProvenanceRequest struct {
	Since string
	Paths []string
}

type ProvenanceEntry struct {
	Path   string             `json:"path"`
	Record *provenance.Record `json:"record,omitempty"`
	Status string             `json:"status"`
}

type ProvenanceTotals struct {
	Files    int            `json:"files"`
	Recorded int            `json:"recorded"`
	Modified int            `json:"modified"`
	Edited   int            `json:"edited"`
	Missing  int            `json:"missing"`
	ByTool   map[string]int `json:"by_tool"`
	ByModel  map[string]int `json:"by_model"`
}

type ProvenanceReport struct {
	Config   bool              `json:"config"`
	Since    string            `json:"since,omitempty"`
	Files    []ProvenanceEntry `json:"files"`
	Orphaned []string          `json:"orphaned"`
	Totals   ProvenanceTotals  `json:"totals"`
}

// ScanProvenance reports the provenance of the working copy's files. The
// record status compares the recorded content hash with the working bytes.
func ScanProvenance(ctx context.Context, d *usecase.Diff, wc *usecase.WorkingCopy, root string, req ProvenanceRequest) (*ProvenanceReport, error) {
	records, err := usecase.LoadProvenance(root)
	if err != nil {
		return nil, err
	}
	out := &ProvenanceReport{Config: records != nil, Since: req.Since, Files: []ProvenanceEntry{}, Orphaned: []string{}}
	if records == nil {
		records = provenance.New()
	}
	prefixes, err := normalizePrefixes(req.Paths)
	if err != nil {
		return nil, err
	}
	candidates, err := provenanceCandidates(ctx, d, wc, root, req.Since, records)
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, p := range candidates {
		if !coveredBy(prefixes, p) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hash, err := usecase.HashWorkingFile(root, p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		present[p] = true
		rec := records.Get(p)
		out.Files = append(out.Files, ProvenanceEntry{Path: p, Record: rec, Status: provenance.Status(rec, hash)})
	}
	for _, p := range records.Paths() {
		if present[p] || !coveredBy(prefixes, p) {
			continue
		}
		if _, err := usecase.HashWorkingFile(root, p); errors.Is(err, fs.ErrNotExist) {
			out.Orphaned = append(out.Orphaned, p)
		}
	}
	out.Totals = provenanceTotals(out.Files)
	return out, nil
}

func provenanceCandidates(ctx context.Context, d *usecase.Diff, wc *usecase.WorkingCopy, root, since string, records *provenance.File) ([]string, error) {
	set := map[string]bool{}
	if since != "" {
		changes, err := Enumerate(ctx, d, root, Request{Revisions: []string{since}})
		if err != nil {
			return nil, err
		}
		for _, c := range changes.Files {
			if c.Item.Status != assetdiff.StatusRemoved {
				set[c.Item.Path] = true
			}
		}
	} else {
		cfg, err := assetbudget.Load(root)
		if err != nil {
			return nil, err
		}
		tracked, err := wc.TrackedPaths()
		if err != nil {
			return nil, err
		}
		for _, p := range tracked {
			if cfg.RequiresProvenance(p) {
				set[p] = true
			}
		}
		for _, p := range records.Paths() {
			set[p] = true
		}
	}
	delete(set, provenance.FileName)
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func normalizePrefixes(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		n, err := domain.NormalizePathPrefix(p)
		if err != nil {
			return nil, err
		}
		if n == "" {
			return nil, nil
		}
		out = append(out, n)
	}
	return out, nil
}

func coveredBy(prefixes []string, p string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, prefix := range prefixes {
		if domain.PrefixCovers(prefix, p) {
			return true
		}
	}
	return false
}

func provenanceTotals(files []ProvenanceEntry) ProvenanceTotals {
	t := ProvenanceTotals{Files: len(files), ByTool: map[string]int{}, ByModel: map[string]int{}}
	for _, f := range files {
		switch f.Status {
		case provenance.StatusRecorded:
			t.Recorded++
		case provenance.StatusModified:
			t.Modified++
		case provenance.StatusEdited:
			t.Edited++
		default:
			t.Missing++
		}
		if f.Record == nil {
			continue
		}
		if f.Record.Tool != "" {
			t.ByTool[f.Record.Tool]++
		}
		if f.Record.Model != "" {
			t.ByModel[f.Record.Model]++
		}
	}
	return t
}
