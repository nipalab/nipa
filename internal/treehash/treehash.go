// Package treehash defines the deterministic content hashing scheme used for
// tree nodes and commits. Hashes are content-addressed: identical inputs
// always produce identical hashes, independent of where the object lives.
package treehash

import (
	"encoding/binary"
	"sort"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
)

// FileHash returns the hash of a file given its ordered chunk hashes:
// BLAKE3(chunk_hash_1 || chunk_hash_2 || ...).
func FileHash(chunkHashes []domain.Hash) domain.Hash {
	return chunker.FileHash(chunkHashes)
}

// childKinds distinguishes file entries from tree entries in the serialized
// form used by TreeHash. A file's mode class is folded into its kind: a
// read-write file keeps kind 0, so trees holding only read-write files hash
// exactly as they did before modes were part of the hash, and executable and
// read-only files get kinds of their own.
const (
	fileKind           byte = 0
	treeKind           byte = 1
	executableFileKind byte = 2
	readOnlyFileKind   byte = 3
)

// FileEntry is a file child of a directory, used when hashing trees. Mode is
// the stored mode; it is classified with domain.ClassifyStoredMode, so any
// representation of the same class (0o755, 0o775, legacy decimal 755) hashes
// the same.
type FileEntry struct {
	Name string
	Hash domain.Hash
	Mode int
}

// fileKindFor returns the serialized kind of a file with the given stored mode.
func fileKindFor(mode int) byte {
	switch domain.ClassifyStoredMode(mode) {
	case domain.FileModeClassExecutable:
		return executableFileKind
	case domain.FileModeClassReadOnly:
		return readOnlyFileKind
	default:
		return fileKind
	}
}

// TreeEntry is a subdirectory child of a directory, used when hashing trees.
type TreeEntry struct {
	Name string
	Hash domain.Hash
}

// TreeHash returns the hash of a directory given its sorted contents.
// Serialization, per child sorted by name:
//
//	kind (1 byte) | name length (4 bytes BE) | name | child hash (32 bytes)
//
// where kind is 0 for a read-write file, 1 for a tree, 2 for an executable file
// and 3 for a read-only file. Files come first, then trees. The whole stream
// is hashed with BLAKE3.
func TreeHash(files []FileEntry, trees []TreeEntry) domain.Hash {
	h := newHasher()
	hashFiles(h, files)
	hashTrees(h, trees)
	return h.finish()
}

// CommitHash returns the hash of a commit:
//
//	BLAKE3(tree_hash (32) || parent count (4 BE) || parent hashes (32 each) || message length (4 BE) || message)
func CommitHash(treeHash domain.Hash, parentHashes []domain.Hash, message string) domain.Hash {
	return commitHasher(treeHash, parentHashes, message).finish()
}

func CommitHashWithOrigin(treeHash domain.Hash, parentHashes []domain.Hash, message, origin string) domain.Hash {
	h := commitHasher(treeHash, parentHashes, message)
	if origin != "" {
		var buf [4]byte
		binary.BigEndian.PutUint32(buf[:], uint32(len(origin)))
		h.write(buf[:])
		h.write([]byte(origin))
	}
	return h.finish()
}

func commitHasher(treeHash domain.Hash, parentHashes []domain.Hash, message string) *hasher {
	h := newHasher()
	h.write(treeHash[:])

	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(len(parentHashes)))
	h.write(buf[:])
	for _, p := range parentHashes {
		h.write(p[:])
	}

	binary.BigEndian.PutUint32(buf[:], uint32(len(message)))
	h.write(buf[:])
	h.write([]byte(message))
	return h
}

type hasher struct {
	chunks []byte
}

func newHasher() *hasher {
	return &hasher{}
}

func (h *hasher) write(b []byte) {
	h.chunks = append(h.chunks, b...)
}

func (h *hasher) finish() domain.Hash {
	return chunker.Sum(h.chunks)
}

func hashFiles(h *hasher, files []FileEntry) {
	sorted := make([]FileEntry, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var buf [4]byte
	for _, f := range sorted {
		h.write([]byte{fileKindFor(f.Mode)})
		binary.BigEndian.PutUint32(buf[:], uint32(len(f.Name)))
		h.write(buf[:])
		h.write([]byte(f.Name))
		h.write(f.Hash[:])
	}
}

func hashTrees(h *hasher, trees []TreeEntry) {
	sorted := make([]TreeEntry, len(trees))
	copy(sorted, trees)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var buf [4]byte
	for _, t := range sorted {
		h.write([]byte{treeKind})
		binary.BigEndian.PutUint32(buf[:], uint32(len(t.Name)))
		h.write(buf[:])
		h.write([]byte(t.Name))
		h.write(t.Hash[:])
	}
}
