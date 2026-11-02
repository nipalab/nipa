package localrepo

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type testFile struct {
	hash   byte
	chunk  byte
	size   int64
	mode   int
	binary bool
	enc    string
}

func tf(hash byte) testFile {
	return testFile{hash: hash, chunk: hash + 100, size: int64(hash), mode: 0o644, enc: "raw"}
}

func pathHash(prefix byte, path string) serverDomain.Hash {
	h := serverDomain.Hash{prefix}
	for i := 0; i < len(path); i++ {
		h[1+i%30] ^= path[i]
	}
	return h
}

// testTree builds a recursive manifest from slash-separated file paths. Every
// directory hash is derived from its path and the given version, so a version
// bump changes the hashes of the whole chain like a real content change would.
func testTree(rootHash byte, files map[string]testFile) *serverDomain.TreeNode {
	root := &serverDomain.TreeNode{Hash: serverDomain.Hash{rootHash}, Name: "root"}
	dirs := map[string]*serverDomain.TreeNode{"": root}
	var ensure func(dir string) *serverDomain.TreeNode
	ensure = func(dir string) *serverDomain.TreeNode {
		if node, ok := dirs[dir]; ok {
			return node
		}
		parent := ""
		name := dir
		if i := strings.LastIndex(dir, "/"); i >= 0 {
			parent, name = dir[:i], dir[i+1:]
		}
		node := &serverDomain.TreeNode{Hash: pathHash(0x70, dir), Name: name}
		dirs[dir] = node
		p := ensure(parent)
		p.TreeChildren = append(p.TreeChildren, node)
		return node
	}

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		dir, name := "", p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			dir, name = p[:i], p[i+1:]
		}
		spec := files[p]
		parent := ensure(dir)
		parent.FileChildren = append(parent.FileChildren, &serverDomain.File{
			Name:      name,
			Hash:      serverDomain.Hash{spec.hash},
			Mode:      spec.mode,
			SizeBytes: spec.size,
			IsBinary:  spec.binary,
			Encoding:  spec.enc,
			Chunks:    []serverDomain.Chunk{{Hash: serverDomain.Hash{spec.chunk}, SizeBytes: spec.size}},
		})
	}
	return root
}

type storedState struct {
	treeHash string
	nodes    []string
	files    []string
}

func readState(t *testing.T, lr *LocalRepo) storedState {
	t.Helper()
	var st storedState
	require.NoError(t, lr.db.QueryRow(`SELECT COALESCE((SELECT value FROM meta WHERE key='tree_hash'), '')`).Scan(&st.treeHash))

	rows, err := lr.db.Query(`SELECT path, parent_path, hex(hash), mode FROM tree_nodes ORDER BY path`)
	require.NoError(t, err)
	for rows.Next() {
		var path, parent, hash string
		var mode int
		require.NoError(t, rows.Scan(&path, &parent, &hash, &mode))
		st.nodes = append(st.nodes, fmt.Sprintf("%s|%s|%s|%d", path, parent, hash, mode))
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	rows, err = lr.db.Query(`SELECT path, tree_path, hex(hash), size_bytes, mode, is_binary, encoding, hex(chunks) FROM files ORDER BY path`)
	require.NoError(t, err)
	for rows.Next() {
		var path, tree, hash, enc, chunks string
		var size int64
		var mode int
		var binary bool
		require.NoError(t, rows.Scan(&path, &tree, &hash, &size, &mode, &binary, &enc, &chunks))
		st.files = append(st.files, fmt.Sprintf("%s|%s|%s|%d|%d|%v|%s|%s", path, tree, hash, size, mode, binary, enc, chunks))
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	return st
}

func freshState(t *testing.T, tree *serverDomain.TreeNode) storedState {
	t.Helper()
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.SaveTree(tree))
	return readState(t, lr)
}

func snapshotPaths(t *testing.T, lr *LocalRepo) []string {
	t.Helper()
	snap, err := lr.Snapshot()
	require.NoError(t, err)
	paths := make([]string, 0, len(snap.Files))
	for _, f := range snap.Files {
		paths = append(paths, f.Path)
	}
	return paths
}

func TestSaveTree_IncrementalMatchesFreshSave(t *testing.T) {
	base := map[string]testFile{
		"README.md":          tf(1),
		"src/main.go":        tf(2),
		"src/util/strs.go":   tf(3),
		"assets/logo.png":    {hash: 4, chunk: 104, size: 4, mode: 0o644, binary: true, enc: "raw"},
		"assets/ui/icon.png": {hash: 5, chunk: 105, size: 5, mode: 0o644, binary: true, enc: "raw"},
	}
	with := func(mut func(map[string]testFile)) map[string]testFile {
		out := make(map[string]testFile, len(base))
		for k, v := range base {
			out[k] = v
		}
		mut(out)
		return out
	}
	changed := func(f testFile, mut func(*testFile)) testFile { mut(&f); return f }

	steps := []struct {
		name  string
		root  byte
		files map[string]testFile
	}{
		{"initial", 1, base},
		{"identical save", 1, base},
		{"modify one file", 2, with(func(m map[string]testFile) { m["src/main.go"] = tf(20) })},
		{"change only the mode", 3, with(func(m map[string]testFile) {
			m["src/main.go"] = changed(tf(20), func(f *testFile) { f.mode = 0o755 })
		})},
		{"change only the chunks", 4, with(func(m map[string]testFile) {
			m["src/main.go"] = changed(tf(20), func(f *testFile) { f.mode = 0o755; f.chunk = 77 })
		})},
		{"change encoding and binary flag", 5, with(func(m map[string]testFile) {
			m["src/main.go"] = changed(tf(20), func(f *testFile) { f.mode = 0o755; f.chunk = 77; f.enc = "zstd"; f.binary = true })
		})},
		{"add files and a new directory", 6, with(func(m map[string]testFile) {
			m["docs/guide/intro.md"] = tf(30)
			m["docs/readme.txt"] = tf(31)
		})},
		{"remove a file", 7, with(func(m map[string]testFile) { delete(m, "README.md") })},
		{"remove a whole subtree", 8, with(func(m map[string]testFile) {
			delete(m, "assets/logo.png")
			delete(m, "assets/ui/icon.png")
		})},
		{"file becomes a directory", 9, with(func(m map[string]testFile) {
			delete(m, "README.md")
			m["README.md/part1.md"] = tf(40)
		})},
		{"directory becomes a file", 10, with(func(m map[string]testFile) { delete(m, "src/util/strs.go"); m["src/util"] = tf(41) })},
		{"move a file between directories", 11, with(func(m map[string]testFile) {
			delete(m, "src/main.go")
			m["assets/main.go"] = tf(2)
		})},
		{"shrink to a single file", 12, map[string]testFile{"only.txt": tf(50)}},
		{"back to the base tree", 13, base},
	}

	lr := newTestLocalRepo(t)
	for _, step := range steps {
		tree := testTree(step.root, step.files)
		require.NoError(t, lr.SaveTree(tree), step.name)
		require.Equal(t, freshState(t, tree), readState(t, lr), step.name)

		wantPaths := make([]string, 0, len(step.files))
		for p := range step.files {
			wantPaths = append(wantPaths, p)
		}
		sort.Strings(wantPaths)
		require.Equal(t, wantPaths, snapshotPaths(t, lr), step.name)
	}

	require.NoError(t, lr.SaveTree(nil))
	require.Equal(t, storedState{treeHash: ""}, readState(t, lr))
	require.Empty(t, snapshotPaths(t, lr))
}

func TestSaveTree_DoesNotTrustTreeHashes(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.SaveTree(testTree(1, map[string]testFile{"a/x.txt": tf(1), "b/y.txt": tf(2)})))

	// Same root hash, different content: sparse, filtered or locally built trees
	// do not guarantee that equal hashes mean equal rows.
	edited := testTree(1, map[string]testFile{"a/x.txt": tf(9), "b/y.txt": tf(2), "b/z.txt": tf(3)})
	require.NoError(t, lr.SaveTree(edited))
	require.Equal(t, freshState(t, edited), readState(t, lr))
}

func TestSaveTree_WritesOnlyChangedRows(t *testing.T) {
	files := map[string]testFile{}
	for d := 0; d < 5; d++ {
		for f := 0; f < 5; f++ {
			files[fmt.Sprintf("d%d/f%d.txt", d, f)] = tf(byte(d*10 + f + 1))
		}
	}

	t.Run("identical tree writes nothing", func(t *testing.T) {
		lr := newTestLocalRepo(t)
		tree := testTree(1, files)
		require.NoError(t, lr.SaveTree(tree))

		createTrigger(t, lr.db, "no_node_writes", "tree_nodes", "BEFORE INSERT", "")
		createTrigger(t, lr.db, "no_file_writes", "files", "BEFORE INSERT", "")
		createTrigger(t, lr.db, "no_node_deletes", "tree_nodes", "BEFORE DELETE", "")
		createTrigger(t, lr.db, "no_file_deletes", "files", "BEFORE DELETE", "")
		require.NoError(t, lr.SaveTree(testTree(1, files)))
	})

	t.Run("one changed file writes that file and its ancestors only", func(t *testing.T) {
		lr := newTestLocalRepo(t)
		require.NoError(t, lr.SaveTree(testTree(1, files)))

		createTrigger(t, lr.db, "only_changed_file", "files", "BEFORE INSERT", "NEW.path <> '/d2/f3.txt'")
		createTrigger(t, lr.db, "only_ancestor_nodes", "tree_nodes", "BEFORE INSERT", "NEW.path NOT IN ('', '/d2')")
		createTrigger(t, lr.db, "no_deletes_files", "files", "BEFORE DELETE", "")
		createTrigger(t, lr.db, "no_deletes_nodes", "tree_nodes", "BEFORE DELETE", "")

		changed := map[string]testFile{}
		for k, v := range files {
			changed[k] = v
		}
		changed["d2/f3.txt"] = tf(200)
		next := testTree(2, changed)
		// Directories on the path to the change get new hashes, like in a real tree.
		next.TreeChildren[2].Hash = serverDomain.Hash{0x7f}
		require.NoError(t, lr.SaveTree(next))
		require.Equal(t, 25, countRows(t, lr.db, "files"))
	})

	t.Run("a removed file deletes exactly one row", func(t *testing.T) {
		lr := newTestLocalRepo(t)
		require.NoError(t, lr.SaveTree(testTree(1, files)))

		createTrigger(t, lr.db, "only_target_delete", "files", "BEFORE DELETE", "OLD.path <> '/d1/f1.txt'")
		createTrigger(t, lr.db, "no_node_deletes2", "tree_nodes", "BEFORE DELETE", "")
		createTrigger(t, lr.db, "no_file_writes2", "files", "BEFORE INSERT", "")

		smaller := map[string]testFile{}
		for k, v := range files {
			smaller[k] = v
		}
		delete(smaller, "d1/f1.txt")
		next := testTree(2, smaller)
		require.NoError(t, lr.SaveTree(next))
		require.Equal(t, 24, countRows(t, lr.db, "files"))
	})
}

func TestSaveTree_StatCacheFollowsIncrementalSaves(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.SaveTree(testTree(1, map[string]testFile{"a.txt": tf(1), "b.txt": tf(2)})))
	require.NoError(t, lr.SaveStatEntries(map[string]domain.StatEntry{
		"a.txt": statEntry(0xaa, 1),
		"b.txt": statEntry(0xbb, 2),
	}))

	require.NoError(t, lr.SaveTree(testTree(2, map[string]testFile{"a.txt": tf(1)})))

	got, err := lr.LoadStatCache()
	require.NoError(t, err)
	require.Contains(t, got, "a.txt")
	require.NotContains(t, got, "b.txt", "a file that left the tree loses its fingerprint")
}

func TestSaveTree_SyncErrors(t *testing.T) {
	seed := func(t *testing.T) *LocalRepo {
		lr := newTestLocalRepo(t)
		require.NoError(t, lr.SaveTree(testTree(1, map[string]testFile{"a/x.txt": tf(1), "b/y.txt": tf(2)})))
		return lr
	}

	t.Run("listing nodes", func(t *testing.T) {
		lr := seed(t)
		_, err := lr.db.Exec(`ALTER TABLE tree_nodes RENAME TO tree_nodes_gone`)
		require.NoError(t, err)
		require.Error(t, lr.SaveTree(testTree(2, map[string]testFile{"a/x.txt": tf(1)})))
	})

	t.Run("deleting a node", func(t *testing.T) {
		lr := seed(t)
		createTrigger(t, lr.db, "no_node_delete_sync", "tree_nodes", "BEFORE DELETE", "")
		require.Error(t, lr.SaveTree(testTree(2, map[string]testFile{"a/x.txt": tf(1)})))
		require.Equal(t, 3, countRows(t, lr.db, "tree_nodes"), "rolled back")
	})

	t.Run("deleting a file", func(t *testing.T) {
		lr := seed(t)
		createTrigger(t, lr.db, "no_file_delete_sync", "files", "BEFORE DELETE", "")
		require.Error(t, lr.SaveTree(testTree(2, map[string]testFile{"a/x.txt": tf(1)})))
		require.Equal(t, 2, countRows(t, lr.db, "files"), "rolled back")
	})
}
