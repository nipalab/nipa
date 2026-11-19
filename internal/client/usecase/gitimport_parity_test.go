package usecase

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/gitimport"
)

func TestGitImportMatchesPushHashes(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	random := make([]byte, 2<<20)
	rng.Read(random)
	random[10] = 0
	var large bytes.Buffer
	for large.Len() < chunker.TextSingleBlobRawMax+(512<<10) {
		large.WriteString("row ")
		large.WriteString(time.Duration(rng.Int63()).String())
		large.WriteByte('\n')
	}
	type spec struct {
		data []byte
		exec bool
	}
	files := map[string]spec{
		"README.md":         {data: []byte("# game\n")},
		"tools/build.sh":    {data: []byte("#!/bin/sh\nmake\n"), exec: true},
		"art/hero.png":      {data: random},
		"data/level.bin":    {data: random[:300<<10]},
		"data/big.csv":      {data: large.Bytes()},
		"empty.txt":         {data: nil},
		"scenes/main.blend": {data: append([]byte{0}, random[:100<<10]...)},
	}

	root := t.TempDir()
	repo, err := git.PlainInit(t.TempDir(), true)
	require.NoError(t, err)
	store := func(o object.Object) plumbing.Hash {
		obj := repo.Storer.NewEncodedObject()
		require.NoError(t, o.Encode(obj))
		h, err := repo.Storer.SetEncodedObject(obj)
		require.NoError(t, err)
		return h
	}
	blob := func(data []byte) plumbing.Hash {
		obj := repo.Storer.NewEncodedObject()
		obj.SetType(plumbing.BlobObject)
		w, err := obj.Writer()
		require.NoError(t, err)
		_, err = w.Write(data)
		require.NoError(t, err)
		require.NoError(t, w.Close())
		h, err := repo.Storer.SetEncodedObject(obj)
		require.NoError(t, err)
		return h
	}
	dirs := map[string][]object.TreeEntry{}
	var paths []string
	for p, s := range files {
		paths = append(paths, p)
		abs := filepath.Join(root, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, s.data, 0o644))
		mode := filemode.Regular
		perm := os.FileMode(0o644)
		if s.exec {
			mode, perm = filemode.Executable, 0o755
		}
		require.NoError(t, os.Chmod(abs, perm))
		dir, name := filepath.Split(p)
		dirs[dir] = append(dirs[dir], object.TreeEntry{Name: name, Mode: mode, Hash: blob(s.data)})
	}
	sort.Strings(paths)
	var rootEntries []object.TreeEntry
	for dir, entries := range dirs {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		if dir == "" {
			rootEntries = append(rootEntries, entries...)
			continue
		}
		rootEntries = append(rootEntries, object.TreeEntry{Name: dir[:len(dir)-1], Mode: filemode.Dir, Hash: store(&object.Tree{Entries: entries})})
	}
	sort.Slice(rootEntries, func(i, j int) bool {
		key := func(e object.TreeEntry) string {
			if e.Mode == filemode.Dir {
				return e.Name + "/"
			}
			return e.Name
		}
		return key(rootEntries[i]) < key(rootEntries[j])
	})
	sig := object.Signature{Name: "Ada", Email: "ada@example.com", When: time.Unix(1700000000, 0)}
	commit := store(&object.Commit{Author: sig, Committer: sig, Message: "import", TreeHash: store(&object.Tree{Entries: rootEntries})})
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference("refs/heads/main", commit)))

	src := gitimport.NewSource(repo)
	plan, err := gitimport.NewPlanner(src).Plan(gitimport.Options{})
	require.NoError(t, err)
	conv, err := gitimport.NewConverter(src, gitimport.ConverterOptions{})
	require.NoError(t, err)
	ev, err := conv.Convert(plan.Commits[0])
	require.NoError(t, err)
	imported := map[string]gitimport.File{}
	for _, f := range ev.Files {
		imported[f.Path] = f
	}

	staged, removed, err := classifyStaged(root, paths, nil)
	require.NoError(t, err)
	require.Empty(t, removed)
	require.Len(t, imported, len(staged))
	for _, sf := range staged {
		collector := newPlanCollector()
		pushed, err := scanPushFile(sf, collector)
		require.NoError(t, err)
		got := imported[sf.path]
		require.Equal(t, *pushed, got.PushFile, sf.path)
		for i, h := range got.ChunkHashes {
			require.Equal(t, collector.sizes[h], got.ChunkSizes[i], sf.path)
		}
	}
}
