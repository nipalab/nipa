package usecase

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

const nipaDir = ".nipa"

type WorkingCopyRepo interface {
	Init(target string) error
	Snapshot() (*domain.Snapshot, error)
	ListStaged() ([]string, error)
	StageAdd(path string) error
	StageRemove(paths []string) error
}

type WorkingCopy struct {
	localRepo WorkingCopyRepo
	root      string
}

func NewWorkingCopy(localRepo WorkingCopyRepo, root string) (*WorkingCopy, error) {
	if err := localRepo.Init(root); err != nil {
		return nil, err
	}
	return &WorkingCopy{localRepo: localRepo, root: root}, nil
}

func (w *WorkingCopy) Add(ctx context.Context, targets []string) error {
	paths, err := w.expandAddTargets(targets)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := w.localRepo.StageAdd(path); err != nil {
			return err
		}
	}
	return nil
}

func (w *WorkingCopy) Remove(ctx context.Context, targets []string) error {
	stagedByPath, err := w.listStagedSet()
	if err != nil {
		return err
	}

	removed := make(map[string]bool)
	for _, t := range targets {
		if err := validateRelPath(t); err != nil {
			return err
		}
		if isNipaPath(t) {
			return fmt.Errorf("cannot operate on path inside %q", nipaDir)
		}
		if t == "" {
			for p := range stagedByPath {
				removed[p] = true
			}
			continue
		}
		if stagedByPath[t] {
			removed[t] = true
			continue
		}
		prefix := t + "/"
		for p := range stagedByPath {
			if strings.HasPrefix(p, prefix) {
				removed[p] = true
			}
		}
	}
	paths := make([]string, 0, len(removed))
	for p := range removed {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil
	}
	return w.localRepo.StageRemove(paths)
}

func (w *WorkingCopy) Status(ctx context.Context) (*domain.Status, error) {
	snapshot, err := w.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	staged, err := w.localRepo.ListStaged()
	if err != nil {
		return nil, err
	}
	baseByPath := make(map[string]domain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		baseByPath[f.Path] = f
	}
	stagedByPath := make(map[string]bool, len(staged))
	for _, p := range staged {
		stagedByPath[p] = true
	}

	var working []string
	err = filepath.WalkDir(w.root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(w.root, p)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if d.Name() == nipaDir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		working = append(working, relSlash)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(working)
	workingSet := make(map[string]bool, len(working))
	for _, p := range working {
		workingSet[p] = true
	}

	st := &domain.Status{Staged: staged}
	for _, path := range working {
		if stagedByPath[path] {
			continue
		}
		base, inBase := baseByPath[path]
		if !inBase {
			st.Untracked = append(st.Untracked, path)
			continue
		}
		got, err := w.workingFileHash(path)
		if err != nil {
			continue
		}
		if got != base.Hash {
			st.Modified = append(st.Modified, path)
		}
	}
	for _, f := range snapshot.Files {
		if !workingSet[f.Path] {
			st.Missing = append(st.Missing, f.Path)
		}
	}
	return st, nil
}

func (w *WorkingCopy) listStagedSet() (map[string]bool, error) {
	staged, err := w.localRepo.ListStaged()
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(staged))
	for _, p := range staged {
		set[p] = true
	}
	return set, nil
}

func (w *WorkingCopy) expandAddTargets(targets []string) ([]string, error) {
	var out []string
	seen := make(map[string]bool)
	for _, t := range targets {
		if err := validateRelPath(t); err != nil {
			return nil, err
		}
		if isNipaPath(t) {
			return nil, fmt.Errorf("cannot operate on path inside %q", nipaDir)
		}
		abs := w.root
		if t != "" {
			abs = filepath.Join(w.root, filepath.FromSlash(t))
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("path %q does not exist", t)
		}
		if !info.IsDir() {
			if !seen[t] {
				out = append(out, t)
				seen[t] = true
			}
			continue
		}
		err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.Name() == nipaDir {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(w.root, p)
			if err != nil {
				return err
			}
			relSlash := filepath.ToSlash(rel)
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			if !seen[relSlash] {
				out = append(out, relSlash)
				seen[relSlash] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func (w *WorkingCopy) workingFileHash(path string) (serverDomain.Hash, error) {
	data, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(path)))
	if err != nil {
		return serverDomain.Hash{}, err
	}
	hash, _, err := chunkFile(data)
	return hash, err
}

func chunkFile(data []byte) (serverDomain.Hash, []serverDomain.Chunk, error) {
	chunks, err := chunker.ChunkAll(data)
	if err != nil {
		return serverDomain.Hash{}, nil, err
	}
	hashes := make([]serverDomain.Hash, len(chunks))
	wrapped := make([]serverDomain.Chunk, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
		wrapped[i] = serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))}
	}
	return chunker.FileHash(hashes), wrapped, nil
}

func validateRelPath(t string) error {
	if t == "" {
		return nil
	}
	if filepath.IsAbs(t) {
		return fmt.Errorf("path %q must be relative to the repository root", t)
	}
	clean := filepath.Clean(t)
	if clean == "." || clean == ".." {
		return fmt.Errorf("path %q is outside the repository", t)
	}
	if strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q is outside the repository", t)
	}
	return nil
}

func isNipaPath(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == nipaDir {
			return true
		}
	}
	return false
}
