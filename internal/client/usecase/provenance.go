package usecase

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/provenance"
)

const maxProvenanceScanBytes = 512 << 20

// ProvenanceReport counts the provenance records an add or remove touched.
type ProvenanceReport struct {
	Recorded int
	Moved    int
	Deleted  int
}

func (r ProvenanceReport) Changed() bool {
	return r.Recorded+r.Moved+r.Deleted > 0
}

func (r ProvenanceReport) String() string {
	return fmt.Sprintf("provenance: recorded %d, moved %d, deleted %d", r.Recorded, r.Moved, r.Deleted)
}

// MaxProvenanceFileBytes caps the .nipaprovenance.yml read from any side.
const MaxProvenanceFileBytes = 16 << 20

// LoadProvenance reads the clone's .nipaprovenance.yml; nil when absent,
// which means the repository has not opted in.
func LoadProvenance(root string) (*provenance.File, error) {
	file, err := os.Open(filepath.Join(root, provenance.FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, MaxProvenanceFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxProvenanceFileBytes {
		return nil, domain.NewUserError(fmt.Sprintf("%s is larger than %d bytes", provenance.FileName, MaxProvenanceFileBytes))
	}
	f, err := provenance.Parse(data)
	if err != nil {
		return nil, domain.NewUserError(fmt.Sprintf("%s: %v", provenance.FileName, err))
	}
	return f, nil
}

// SaveProvenance writes the provenance file atomically.
func SaveProvenance(root string, f *provenance.File) error {
	tmp, err := os.CreateTemp(root, ".nipa-provenance-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(f.Marshal()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	target := filepath.Join(root, provenance.FileName)
	perm := materializedMode(0)
	if info, err := os.Stat(target); err == nil {
		perm = info.Mode().Perm()
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	return replaceFile(name, target)
}

// HashWorkingFile returns the provenance content hash of a working file.
func HashWorkingFile(root, p string) (string, error) {
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	return provenance.HashContent(file)
}

func provenanceNow() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

func (w *WorkingCopy) workingFileInfo(p string) (os.FileInfo, bool, error) {
	info, err := os.Stat(filepath.Join(w.root, filepath.FromSlash(p)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return info, true, nil
}

// recordProvenance fills records for the paths about to be staged: records
// of files gone from the working copy follow a new path with the same
// content, new supported files get the metadata they declare, and staged
// deletions drop what is left. Existing records are never overwritten.
func (w *WorkingCopy) recordProvenance(snapshot *domain.Snapshot, paths []string) (ProvenanceReport, bool, error) {
	var rep ProvenanceReport
	f, err := LoadProvenance(w.root)
	if err != nil || f == nil {
		return rep, false, err
	}
	tracked := map[string]bool{}
	for _, p := range snapshotPaths(snapshot) {
		tracked[p] = true
	}
	orphans := map[string][]string{}
	for _, p := range f.Paths() {
		if !tracked[p] {
			continue
		}
		if _, exists, err := w.workingFileInfo(p); err != nil {
			return rep, false, err
		} else if !exists {
			hash := f.Files[p].ContentHash
			orphans[hash] = append(orphans[hash], p)
		}
	}
	gone := map[string]bool{}
	for _, p := range paths {
		if p == provenance.FileName {
			continue
		}
		info, exists, err := w.workingFileInfo(p)
		if err != nil {
			return rep, false, err
		}
		if !exists {
			gone[p] = true
			continue
		}
		if f.Get(p) != nil || !info.Mode().IsRegular() || info.Size() > maxProvenanceScanBytes {
			continue
		}
		if len(orphans) == 0 && !provenance.Supported(p) {
			continue
		}
		hash, err := HashWorkingFile(w.root, p)
		if err != nil {
			return rep, false, err
		}
		if from := orphans[hash]; len(from) > 0 {
			f.Move(from[0], p)
			if orphans[hash] = from[1:]; len(orphans[hash]) == 0 {
				delete(orphans, hash)
			}
			rep.Moved++
			continue
		}
		if rec := w.extractRecord(p); rec != nil {
			rec.ContentHash, rec.RecordedAt = hash, provenanceNow()
			if f.Set(p, rec) == nil {
				rep.Recorded++
			}
		}
	}
	for p := range gone {
		if f.Delete(p) {
			rep.Deleted++
		}
	}
	if !rep.Changed() {
		return rep, false, nil
	}
	return rep, true, SaveProvenance(w.root, f)
}

func (w *WorkingCopy) extractRecord(p string) *provenance.Record {
	if !provenance.Supported(p) {
		return nil
	}
	file, err := os.Open(filepath.Join(w.root, filepath.FromSlash(p)))
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	rec, ok, err := provenance.Extract(p, file, provenance.Limits{})
	if err != nil || !ok {
		return nil
	}
	return rec
}

// forgetProvenance drops the records of new files that are unstaged again,
// undoing what add recorded. Tracked files keep their records.
func (w *WorkingCopy) forgetProvenance(paths []string) (ProvenanceReport, bool, error) {
	var rep ProvenanceReport
	if slices.Contains(paths, provenance.FileName) {
		return rep, false, nil
	}
	f, err := LoadProvenance(w.root)
	if err != nil || f == nil {
		return rep, false, err
	}
	snapshot, err := w.localRepo.Snapshot()
	if err != nil {
		return rep, false, err
	}
	tracked := make(map[string]bool, len(snapshot.Files))
	for _, sf := range snapshot.Files {
		tracked[sf.Path] = true
	}
	for _, p := range paths {
		if !tracked[p] && f.Delete(p) {
			rep.Deleted++
		}
	}
	if !rep.Changed() {
		return rep, false, nil
	}
	return rep, true, SaveProvenance(w.root, f)
}

// SnapshotPathSet returns the paths of the pinned snapshot. In a sparse or
// read-filtered clone it holds only the materialized set.
func (w *WorkingCopy) SnapshotPathSet() (map[string]bool, error) {
	snapshot, err := w.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, p := range snapshotPaths(snapshot) {
		out[p] = true
	}
	return out, nil
}

// TrackedPaths lists the tracked and staged files present in the working
// copy, sorted.
func (w *WorkingCopy) TrackedPaths() ([]string, error) {
	snapshot, err := w.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	staged, err := w.localRepo.ListStaged()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range append(snapshotPaths(snapshot), staged...) {
		if seen[p] {
			continue
		}
		seen[p] = true
		info, exists, err := w.workingFileInfo(p)
		if err != nil {
			return nil, err
		}
		if exists && info.Mode().IsRegular() {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func withProvenanceFile(paths []string) []string {
	if slices.Contains(paths, provenance.FileName) {
		return paths
	}
	paths = append(paths, provenance.FileName)
	sort.Strings(paths)
	return paths
}
