package gitimport

import (
	"fmt"
)

type PathIssue struct {
	Commit string
	Path   string
}

type LFSObject struct {
	OID    string
	Size   int64
	Commit string
	Path   string
}

type Report struct {
	Commits      int
	Branches     int
	Tags         int
	Blobs        int
	Bytes        int64
	Files        int
	Removed      int
	EmptyCommits []string
	Octopus      []string
	InvalidPaths []PathIssue
	Symlinks     []PathIssue
	Submodules   []Submodule
	Warnings     []Warning
	LFSObjects   int
	LFSBytes     int64
	LFSMissing   []LFSObject
}

func (c *Converter) DryRun(plan *Plan) (*Report, error) {
	r := &Report{
		Commits:  len(plan.Commits),
		Branches: len(plan.Branches()),
		Tags:     len(plan.Tags()),
		Warnings: append([]Warning(nil), plan.Warnings...),
	}
	for _, w := range plan.Warnings {
		if w.Kind == WarnOctopus {
			r.Octopus = append(r.Octopus, w.Commit)
		}
	}
	blobs := map[string]bool{}
	invalid := map[string]bool{}
	symlinks := map[string]bool{}
	submodules := map[string]bool{}
	oids := map[string]bool{}
	for _, pc := range plan.Commits {
		commit, err := c.src.Commit(pc.Hash)
		if err != nil {
			return nil, fmt.Errorf("read commit %s: %w", pc.Hash, err)
		}
		d, err := c.delta(commit, pc)
		if err != nil {
			return nil, err
		}
		if len(d.files)+len(d.removed)+len(d.invalid) == 0 {
			r.EmptyCommits = append(r.EmptyCommits, pc.Hash.String())
		}
		r.Files += len(d.files)
		r.Removed += len(d.removed)
		for _, f := range d.files {
			sha := f.blob.String()
			if blobs[sha] {
				continue
			}
			blobs[sha] = true
			size, err := c.src.repo.Storer.EncodedObjectSize(f.blob)
			if err != nil {
				return nil, fmt.Errorf("size of blob %s: %w", sha, err)
			}
			ptr, isPointer, err := readPointer(c.src, f.blob)
			if err != nil {
				return nil, fmt.Errorf("read blob %s: %w", sha, err)
			}
			if !isPointer {
				r.Bytes += size
				continue
			}
			if c.opts.LFS == nil {
				r.Bytes += size
			}
			if oids[ptr.OID] {
				continue
			}
			oids[ptr.OID] = true
			r.LFSObjects++
			r.LFSBytes += ptr.Size
			if c.opts.LFS == nil {
				continue
			}
			r.Bytes += ptr.Size
			if !c.opts.LFS.Has(ptr) {
				r.LFSMissing = append(r.LFSMissing, LFSObject{OID: ptr.OID, Size: ptr.Size, Commit: pc.Hash.String(), Path: f.path})
			}
		}
		for _, p := range d.invalid {
			if !invalid[p] {
				invalid[p] = true
				r.InvalidPaths = append(r.InvalidPaths, PathIssue{Commit: pc.Hash.String(), Path: p})
			}
		}
		for _, p := range d.symlinks {
			if !symlinks[p] {
				symlinks[p] = true
				r.Symlinks = append(r.Symlinks, PathIssue{Commit: pc.Hash.String(), Path: p})
			}
		}
		for _, s := range d.submodules {
			if !submodules[s.Path] {
				submodules[s.Path] = true
				r.Submodules = append(r.Submodules, s)
			}
		}
	}
	r.Blobs = len(blobs)
	return r, nil
}
