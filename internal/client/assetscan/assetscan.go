// Package assetscan enumerates the changed files of a working copy or of
// revisions as assetdiff items, and checks them against the repository's
// .nipaassets.yml budget. It is shared by the CLI and the MCP server.
package assetscan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nipalab/nipa/internal/assetbudget"
	"github.com/nipalab/nipa/internal/assetdiff"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/provenance"
)

// MaxProvenanceBytes caps the .nipaprovenance.yml read from any side.
const MaxProvenanceBytes = usecase.MaxProvenanceFileBytes

// LoadProvenance reads the provenance file of side through load; a missing
// file is nil and no error.
func LoadProvenance(ctx context.Context, load assetdiff.Loader, side string) (*provenance.File, error) {
	data, err := load(ctx, *SideRef(side, provenance.FileName), MaxProvenanceBytes)
	if errors.Is(err, assetdiff.ErrMissing) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f, err := provenance.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", provenance.FileName, err)
	}
	return f, nil
}

const (
	SideWorking  = "working"
	SideSnapshot = "snapshot"
)

// BlobLoader reads file versions of the clone at root for assetdiff.
func BlobLoader(d *usecase.Diff, root string) assetdiff.Loader {
	return func(ctx context.Context, ref assetdiff.Ref, limit int64) ([]byte, error) {
		var data []byte
		req := usecase.BlobRequest{Source: usecase.BlobSource(ref.Source), Revision: ref.Revision, Path: ref.Path}
		err := d.ReadBlob(ctx, root, req, func(_ diff.Entry, r io.Reader) error {
			var err error
			data, err = io.ReadAll(io.LimitReader(r, limit+1))
			if err == nil && int64(len(data)) > limit {
				err = assetdiff.ErrTooLarge
			}
			return err
		})
		if errors.Is(err, usecase.ErrFileMissing) {
			return nil, assetdiff.ErrMissing
		}
		return data, err
	}
}

// SideRef maps a side name to a file version: working, snapshot, or any
// other token as a revision.
func SideRef(side, p string) *assetdiff.Ref {
	switch side = strings.TrimSpace(side); side {
	case SideWorking, SideSnapshot:
		return &assetdiff.Ref{Source: side, Path: p}
	default:
		return &assetdiff.Ref{Source: string(usecase.BlobRevision), Revision: side, Path: p}
	}
}

// ChangeItem builds the item of one changed path. An empty oldSide (no
// merge base) makes every file an addition.
func ChangeItem(p, oldPath, status, oldSide, newSide string) assetdiff.Item {
	it := assetdiff.Item{Path: p}
	if oldPath == "" {
		oldPath = p
	}
	switch strings.ToUpper(status) {
	case "A", "ADDED":
		it.Status = assetdiff.StatusAdded
	case "D", "DELETED", "REMOVED":
		it.Status = assetdiff.StatusRemoved
	case "R", "RENAMED":
		it.Status = "renamed"
		it.OldPath = oldPath
	default:
		it.Status = assetdiff.StatusModified
	}
	if it.Status != assetdiff.StatusAdded && oldSide != "" {
		it.Old = SideRef(oldSide, oldPath)
	}
	if it.Status != assetdiff.StatusRemoved {
		it.New = SideRef(newSide, p)
	}
	return it
}

// Request selects the compared sides like nipa diff: no revisions compares
// the working tree with the last synced snapshot (offline), one revision
// with the working tree, two revisions with each other.
type Request struct {
	Revisions []string
	Paths     []string
	Staged    bool
	MergeBase bool
	NoCache   bool
}

// Change is one changed file with the size of its new side.
type Change struct {
	Item    assetdiff.Item
	NewSize int64
}

type Changes struct {
	Old   string
	New   string
	Files []Change
}

// Enumerate lists the changed files of req with refs to both sides.
func Enumerate(ctx context.Context, d *usecase.Diff, root string, req Request) (*Changes, error) {
	files, err := d.Run(ctx, root, req.Revisions, usecase.DiffOptions{
		Staged:    req.Staged,
		Paths:     req.Paths,
		MergeBase: req.MergeBase,
		NoCache:   req.NoCache,
	})
	if err != nil {
		return nil, err
	}
	out := &Changes{Old: SideSnapshot, New: SideWorking}
	switch len(req.Revisions) {
	case 1:
		out.Old = req.Revisions[0]
	case 2:
		out.Old, out.New = req.Revisions[0], req.Revisions[1]
	}
	if req.MergeBase && len(req.Revisions) == 2 {
		if out.Old, err = d.MergeBaseCommit(ctx, root, req.Revisions[0], req.Revisions[1]); err != nil {
			return nil, err
		}
	}
	out.Files = make([]Change, 0, len(files))
	for _, f := range files {
		c := f.Change
		out.Files = append(out.Files, Change{
			Item:    ChangeItem(c.Path, c.Old.Path, c.Status.String(), out.Old, out.New),
			NewSize: c.New.SizeBytes,
		})
	}
	return out, nil
}

// Skip is a covered file whose metadata could not be read; only its size
// and name were checked.
type Skip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Report struct {
	Config     bool                    `json:"config"`
	Old        string                  `json:"old,omitempty"`
	New        string                  `json:"new,omitempty"`
	Violations []assetbudget.Violation `json:"violations"`
	Checked    int                     `json:"checked"`
	Skipped    []Skip                  `json:"skipped,omitempty"`
}

// Check loads root's .nipaassets.yml and checks the changed files of req
// against it. The budget always comes from the working copy, even when two
// revisions are compared. Without a budget file the report is empty and
// Config is false.
func Check(ctx context.Context, d *usecase.Diff, root string, req Request) (*Report, error) {
	cfg, err := assetbudget.Load(root)
	if err != nil || cfg == nil {
		return &Report{Violations: []assetbudget.Violation{}}, err
	}
	changes, err := Enumerate(ctx, d, root, req)
	if err != nil {
		return nil, err
	}
	return CheckChanges(ctx, BlobLoader(d, root), cfg, changes)
}

// CheckChanges checks enumerated changes against cfg. Only files a rule
// covers are counted, and only those with image, model or audio limits are
// decoded (new side only). Provenance records come from the new side's
// .nipaprovenance.yml, read only when a covered file requires one.
func CheckChanges(ctx context.Context, load assetdiff.Loader, cfg *assetbudget.Config, changes *Changes) (*Report, error) {
	out := &Report{Config: true, Old: changes.Old, New: changes.New}
	var results []*assetdiff.Result
	needRecords := false
	for _, c := range changes.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		it := c.Item
		if it.New == nil || !cfg.Covers(it.Path) {
			continue
		}
		out.Checked++
		needRecords = needRecords || cfg.RequiresProvenance(it.Path)
		res := &assetdiff.Result{Path: it.Path, Status: it.Status, NewSize: c.NewSize}
		if cfg.NeedsDecode(it.Path) {
			decoded, err := assetdiff.Compare(ctx, load, nil, it.New, assetdiff.Options{})
			switch {
			case ctx.Err() != nil:
				return nil, ctx.Err()
			case errors.Is(err, assetdiff.ErrMissing):
				out.Skipped = append(out.Skipped, Skip{Path: it.Path, Reason: "the file is missing"})
			case err != nil:
				out.Skipped = append(out.Skipped, Skip{Path: it.Path, Reason: err.Error()})
			default:
				decoded.Status = it.Status
				if decoded.NewSize == 0 {
					decoded.NewSize = c.NewSize
				}
				if decoded.Error != "" {
					out.Skipped = append(out.Skipped, Skip{Path: it.Path, Reason: decoded.Error})
				}
				res = decoded
			}
		}
		results = append(results, res)
	}
	var opts assetbudget.CheckOptions
	if needRecords {
		records, err := LoadProvenance(ctx, load, changes.New)
		if err != nil {
			return nil, err
		}
		if records != nil {
			opts.HasRecord = func(p string) bool { return records.Get(p) != nil }
		}
	}
	out.Violations = assetbudget.CheckWith(cfg, results, opts)
	return out, nil
}
