package usecase

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type manifestFetcher interface {
	GetCommitTreeManifest(ctx context.Context, org, project, commitID string, paths []string) (*serverDomain.TreeNode, error)
}

// maxRestoreManifestPaths bounds the path filters one manifest request for a
// restore carries; above it the manifest of the whole checkout is asked for.
var maxRestoreManifestPaths = 256

// restoreDirect rebuilds raw files the object cache does not keep straight in
// place, the way an update writes them: the pinned commit's manifest supplies
// the chunk sizes, chunks the cache holds are copied, chunks the changed
// working file still contains are copied from it by offset (layouts, so the
// file is not read and hashed a second time), and only the rest is
// downloaded, streamed into the file. Nothing passes through the object
// cache, so a restored binary is written once instead of twice.
//
// It returns the fingerprints of the files it restored and the files left for
// the cache path: text, pinned content, files whose content is fully cached,
// and every candidate when the server or its manifest cannot be used (that
// path then reports why).
func (w *WorkingCopy) restoreDirect(ctx context.Context, changed []domain.SnapshotFile, layouts map[string]workingLayout) (map[string]domain.StatEntry, []domain.SnapshotFile, error) {
	entries := map[string]domain.StatEntry{}
	fetcher, ok := w.downloader.(manifestFetcher)
	if !ok || len(changed) == 0 {
		return entries, changed, nil
	}
	pins, err := loadPins(w.localRepo)
	if err != nil {
		return nil, nil, err
	}
	var candidates, rest []domain.SnapshotFile
	for _, f := range changed {
		if len(f.Chunks) > 0 && storedEncoding(f.Encoding) == chunker.EncodingRaw && !pinsCover(pins, f.Path) {
			candidates = append(candidates, f)
		} else {
			rest = append(rest, f)
		}
	}
	p := w.pristine()
	uncached, err := p.missingSet(candidates)
	if err != nil {
		return nil, nil, err
	}
	var need []domain.SnapshotFile
	for _, f := range candidates {
		if needsAny(f, uncached) {
			need = append(need, f)
		} else {
			rest = append(rest, f)
		}
	}
	if len(need) == 0 {
		return entries, changed, nil
	}

	nu, commitID, err := p.connect(ctx)
	if err != nil || commitID == "" {
		return entries, changed, nil
	}
	cfg, err := w.localRepo.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	paths := cfg.Sparse
	if len(need) <= maxRestoreManifestPaths {
		paths = make([]string, 0, len(need))
		for _, f := range need {
			paths = append(paths, f.Path)
		}
	}
	manifest, err := restoreManifest(ctx, fetcher, nu, commitID, need, cfg.Sparse)
	if err != nil {
		return entries, changed, nil
	}
	var jobs []materializeJob
	var estimated int64
	for _, f := range need {
		mf, ok := manifest[f.Path]
		if ok && mf.FileHash == f.Hash && directEligible(mf, nil) {
			jobs = append(jobs, materializeJob{file: mf, prevMode: f.Mode})
			estimated += mf.SizeBytes
		} else {
			rest = append(rest, f)
		}
	}
	if len(jobs) == 0 {
		return entries, changed, nil
	}

	plan, err := newDirectPlan(w.root, jobs)
	if err != nil {
		return nil, nil, err
	}
	defer plan.cleanup()
	wanted := make([]serverDomain.Hash, 0, len(plan.targets))
	for h := range plan.targets {
		wanted = append(wanted, h)
	}
	missingList, err := w.localRepo.MissingChunks(wanted)
	if err != nil {
		return nil, nil, err
	}
	missing := make(map[serverDomain.Hash]struct{}, len(missingList))
	for _, h := range missingList {
		missing[h] = struct{}{}
	}
	local := make([]serverDomain.Hash, 0, len(wanted)-len(missingList))
	for _, h := range wanted {
		if _, gone := missing[h]; !gone {
			local = append(local, h)
		}
	}
	if err := plan.copyLocal(ctx, local, w.loadChunk); err != nil {
		return nil, nil, err
	}
	for _, job := range jobs {
		if err := plan.salvage(w.root, job.file.Path, w.layoutFor(job.file.Path, layouts), missing); err != nil {
			return nil, nil, err
		}
	}

	remaining := make([]serverDomain.Hash, 0, len(missing))
	for h := range missing {
		remaining = append(remaining, h)
	}
	scope := domain.ChunkScope{Org: nu.Org, Project: nu.Project, CommitIDs: commitIDs(commitID), Paths: paths}
	sink := func(c *serverDomain.ChunkData) error { return plan.write(c.Hash, c.Data) }
	stream := &chunkStreamTarget{wants: plan.wants, receive: plan.receive, settle: plan.settle}
	if err := downloadChunksStream(ctx, w.downloader, scope, remaining, estimated, sink, stream); err != nil {
		return nil, nil, offlineError(err)
	}
	if err := plan.finish(ctx); err != nil {
		return nil, nil, err
	}
	for path, entry := range plan.entries {
		entries[path] = entry
	}
	return entries, rest, nil
}

type dirManifestFetcher interface {
	GetCommitDirManifest(ctx context.Context, org, project, commitID, dir string) (*serverDomain.TreeNode, error)
}

// restoreManifest reads the manifest entries of the files in need from the
// commit. The server's path filter takes folders, not files, so a small
// restore asks for the folders its files sit in (the root's own files through
// GetCommitDirManifest when the fetcher has it); a restore of more than
// maxRestoreManifestPaths files, or of root files without that call, asks for
// the whole checkout.
func restoreManifest(ctx context.Context, fetcher manifestFetcher, nu *domain.NipaUrl, commitID string, need []domain.SnapshotFile, sparse []string) (map[string]materializedFile, error) {
	want := make(map[string]struct{}, len(need))
	for _, f := range need {
		want[f.Path] = struct{}{}
	}
	manifest := make(map[string]materializedFile, len(need))
	collect := func(tree *serverDomain.TreeNode, err error) error {
		if err != nil {
			return err
		}
		walkTreeFiles(tree, "", func(path string, f *serverDomain.File) {
			if _, ok := want[path]; ok {
				manifest[path] = materializedFromTree(path, f)
			}
		})
		return nil
	}
	dirFetcher, hasDirs := fetcher.(dirManifestFetcher)
	seen := map[string]struct{}{}
	var dirs []string
	root := false
	for _, f := range need {
		dir := parentDir(f.Path)
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		if dir == "" {
			root = true
		} else {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	if len(need) > maxRestoreManifestPaths || (root && !hasDirs) {
		return manifest, collect(fetcher.GetCommitTreeManifest(ctx, nu.Org, nu.Project, commitID, sparse))
	}
	if root {
		if err := collect(dirFetcher.GetCommitDirManifest(ctx, nu.Org, nu.Project, commitID, "")); err != nil {
			return nil, err
		}
	}
	if len(dirs) > 0 {
		if err := collect(fetcher.GetCommitTreeManifest(ctx, nu.Org, nu.Project, commitID, dirs)); err != nil {
			return nil, err
		}
	}
	return manifest, nil
}

func (w *WorkingCopy) loadChunk(h serverDomain.Hash) ([]byte, error) {
	rc, err := w.localRepo.OpenChunk(h)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// layoutFor returns the layout recorded while the file was hashed, or reads
// it now for a file that was not hashed (only its mode changed), so its
// content is still reused instead of downloaded.
func (w *WorkingCopy) layoutFor(path string, layouts map[string]workingLayout) workingLayout {
	if layout, ok := layouts[path]; ok {
		return layout
	}
	if w.hashFile != nil {
		return workingLayout{}
	}
	info, err := os.Stat(filepath.Join(w.root, filepath.FromSlash(path)))
	if err != nil || !info.Mode().IsRegular() {
		return workingLayout{}
	}
	_, chunks, err := hashWorkingLayout(w.root, path, chunker.EncodingRaw)
	if err != nil {
		return workingLayout{}
	}
	return workingLayout{chunks: chunks, info: info}
}

// salvage copies the chunks of the working file that the plan still misses
// from the file's own bytes, by the offsets its layout recorded, and takes
// them out of missing. The bytes are not hashed again: the copy counts only
// when the file's size and modification time are what they were when the
// layout was read, checked before and after.
func (p *directPlan) salvage(root, path string, layout workingLayout, missing map[serverDomain.Hash]struct{}) error {
	if len(layout.chunks) == 0 || layout.info == nil {
		return nil
	}
	src, err := os.Open(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return nil
	}
	defer func() { _ = src.Close() }()
	if !sameFileState(src, layout.info) {
		return nil
	}
	var offset int64
	for _, c := range layout.chunks {
		start := offset
		offset += c.SizeBytes
		if _, gone := missing[c.Hash]; !gone || !p.wants(c.Hash) {
			continue
		}
		if err := p.copyFrom(c.Hash, src, start, c.SizeBytes); err != nil {
			return err
		}
		delete(missing, c.Hash)
	}
	if !sameFileState(src, layout.info) {
		return &materializeError{path: path, err: fmt.Errorf("%s changed while it was being restored; run the restore again", path)}
	}
	return nil
}

func sameFileState(f *os.File, want os.FileInfo) bool {
	info, err := f.Stat()
	return err == nil && info.Size() == want.Size() && info.ModTime().Equal(want.ModTime())
}

// copyFrom places a chunk that sits at offset in src at every offset the plan
// needs it, then books it.
func (p *directPlan) copyFrom(h serverDomain.Hash, src io.ReaderAt, offset, size int64) error {
	err := eachTargetFile(p.targets[h], func(df *directFile, targets []directTarget) error {
		out, err := df.handles.acquire(df)
		if err != nil {
			return err
		}
		defer df.handles.release(df)
		buf := streamBufferPool.Get().(*[]byte)
		defer streamBufferPool.Put(buf)
		for _, t := range targets {
			if t.size != size {
				return fmt.Errorf("content length mismatch for %s: chunk is %d bytes, want %d", df.job.file.Path, size, t.size)
			}
			if _, err := io.CopyBuffer(io.NewOffsetWriter(out, t.offset), io.NewSectionReader(src, offset, size), *buf); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return p.settle(h)
}
