package assetscan

import (
	"context"
	"os"

	"github.com/nipalab/nipa/internal/assetdiff"
	"github.com/nipalab/nipa/internal/assetrefs"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type RefsReport struct {
	Path      string          `json:"path"`
	Uses      []assetrefs.Ref `json:"uses"`
	UsedBy    []string        `json:"used_by"`
	Unknown   []assetrefs.Ref `json:"unknown"`
	Scanned   int             `json:"scanned"`
	Truncated bool            `json:"truncated,omitempty"`
}

// Graph builds the reference graph of the working copy at root. In a sparse
// clone, references into paths the clone does not hold are unknown.
func Graph(ctx context.Context, d *usecase.Diff, root string, opts assetrefs.Options) (*assetrefs.Graph, error) {
	files, sparse, err := d.WorkingFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	if len(sparse) > 0 {
		opts.Outside = func(p string) bool {
			for _, prefix := range sparse {
				if serverDomain.PrefixCovers(prefix, p) {
					return false
				}
			}
			return true
		}
	}
	return assetrefs.Build(os.DirFS(root), files, opts), nil
}

// Refs reports what the asset at p uses and which files use it.
func Refs(ctx context.Context, d *usecase.Diff, root, p string, opts assetrefs.Options) (*RefsReport, error) {
	g, err := Graph(ctx, d, root, opts)
	if err != nil {
		return nil, err
	}
	out := &RefsReport{Path: p, Uses: []assetrefs.Ref{}, UsedBy: []string{}, Unknown: []assetrefs.Ref{}, Scanned: g.Scanned, Truncated: g.Truncated}
	for _, r := range g.Uses(p) {
		if r.Unknown {
			out.Unknown = append(out.Unknown, r)
		} else {
			out.Uses = append(out.Uses, r)
		}
	}
	out.UsedBy = append(out.UsedBy, g.UsedBy(p)...)
	return out, nil
}

// Relevant reports whether any change can affect asset references.
func Relevant(changes *Changes) bool {
	for _, c := range changes.Files {
		if assetrefs.Relevant(c.Item.Path) || (c.Item.OldPath != "" && assetrefs.Relevant(c.Item.OldPath)) {
			return true
		}
	}
	return false
}

// Impact reports the references the changes break and the assets they leave
// unused. The new side is always the working copy; the old side of each
// change is read through its Old ref. It returns nil when no change touches
// an engine asset, without scanning the working copy.
func Impact(ctx context.Context, d *usecase.Diff, root string, changes *Changes, opts assetrefs.Options) (*assetrefs.Impact, error) {
	if !Relevant(changes) {
		return nil, nil
	}
	g, err := Graph(ctx, d, root, opts)
	if err != nil {
		return nil, err
	}
	items := make([]assetdiff.Item, 0, len(changes.Files))
	for _, c := range changes.Files {
		items = append(items, c.Item)
	}
	return assetrefs.ComputeImpact(ctx, g, items, BlobLoader(d, root))
}
