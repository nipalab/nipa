package suite_test

import (
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nipalab/nipa/internal/chunker"
)

// objectEntries lists every file and folder under .nipa/objects. A prune
// removes objects but keeps their <hash[:2]> fan-out folders, so content that
// passed through the cache leaves a trace even after the restore pruned it.
func objectEntries(dir string) []string {
	GinkgoHelper()
	var out []string
	root := filepath.Join(dir, ".nipa", "objects")
	Expect(filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})).To(Succeed())
	return out
}

// absentChunkFanouts returns the fan-out folders of path's raw chunks that the
// object cache does not have yet: a restore that cached any of those chunks
// creates its folder.
func absentChunkFanouts(dir, path string, content []byte) []string {
	GinkgoHelper()
	_, chunks, err := chunker.EncodeBytes(content, path, true, chunker.EncodingRaw)
	Expect(err).NotTo(HaveOccurred())
	// The binary profile caps a chunk at Max bytes, so the sizes the specs use
	// guarantee several chunks whatever the content-defined cut points are.
	limit := int64(chunker.ConfigForFile(path, true).Max)
	minChunks := (int64(len(content)) + limit - 1) / limit
	Expect(minChunks).To(BeNumerically(">=", 2), "%s must be larger than one chunk can be", path)
	Expect(int64(len(chunks))).To(BeNumerically(">=", minChunks))
	seen := map[string]bool{}
	var absent []string
	for _, c := range chunks {
		fanout := c.Hash.String()[:2]
		if seen[fanout] {
			continue
		}
		seen[fanout] = true
		if _, err := os.Stat(filepath.Join(dir, ".nipa", "objects", fanout)); os.IsNotExist(err) {
			absent = append(absent, fanout)
		}
	}
	return absent
}

func expectNoMaterializeTemps(dir string) {
	GinkgoHelper()
	Expect(filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		Expect(strings.HasPrefix(d.Name(), ".nipa-materialize-")).To(BeFalse(), "leftover temp file %s", path)
		return nil
	})).To(Succeed())
}

// binaryAsset is mib MiB of incompressible bytes led by a NUL, so it is
// tracked as raw binary content. The generator is seeded, so content, chunk
// hashes and their fan-out folders are the same on every run.
func binaryAsset(seed int64, mib int) []byte {
	data := make([]byte, 1+mib<<20)
	_, _ = rand.New(rand.NewSource(seed)).Read(data[1:])
	return data
}

// editTail overwrites the last bytes of a tracked binary, so only its last
// chunk differs from the committed version.
func editTail(dir, path string, content []byte) {
	GinkgoHelper()
	edited := append([]byte(nil), content...)
	copy(edited[len(edited)-16:], "local edit here!")
	writeBytes(dir, path, edited)
}

var _ = Describe("nipa restore of binaries", func() {
	// restoreDirect runs `nipa restore args...` on a fresh clone and checks
	// the direct path wrote the files: content is the committed version, the
	// clone is clean, and no chunk of the restored binaries reached the object
	// cache, not even for the moment before the restore's prune.
	restoreDirect := func(dir string, files map[string][]byte, args ...string) {
		GinkgoHelper()
		var absent []string
		for path, content := range files {
			absent = append(absent, absentChunkFanouts(dir, path, content)...)
		}
		Expect(len(absent)).To(BeNumerically(">=", 2), "the probe needs fan-out folders the clone does not have")
		before := objectEntries(dir)

		res := runNipa(dir, append([]string{"restore"}, args...)...)
		Expect(res.ExitCode).To(Equal(0), res.Output())

		for path, content := range files {
			Expect(readBytes(dir, path)).To(Equal(content), "%s is the committed version", path)
		}
		for _, fanout := range absent {
			Expect(filepath.Join(dir, ".nipa", "objects", fanout)).NotTo(BeADirectory(),
				"a binary chunk passed through the object cache: the restore fell back to the cache path")
		}
		Expect(objectEntries(dir)).To(Equal(before))
		expectNoMaterializeTemps(dir)
		assertCleanStatus(dir)
	}

	It("rebuilds an edited binary at the root without the object cache", func() {
		project := newProject("restore-direct-root")
		content := binaryAsset(1, 6)
		seedRepo(orgSlug, project, map[string][]byte{"big.bin": content, "notes.txt": []byte("notes\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		editTail(dir, "big.bin", content)
		restoreDirect(dir, map[string][]byte{"big.bin": content}, "big.bin")
	})

	It("rebuilds edited binaries in nested folders, one at a time, several at once and by folder", func() {
		project := newProject("restore-direct-nested")
		files := map[string][]byte{
			"art/deep/hero.bin": binaryAsset(2, 5),
			"art/icon.bin":      binaryAsset(3, 5),
			"audio/music.bin":   binaryAsset(4, 5),
			"top.bin":           binaryAsset(5, 5),
		}
		seed := map[string][]byte{"notes.txt": []byte("notes\n")}
		for path, content := range files {
			seed[path] = content
		}
		seedRepo(orgSlug, project, seed, "seed")
		parent := workspace()

		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "one")
		editTail(dir, "art/deep/hero.bin", files["art/deep/hero.bin"])
		restoreDirect(dir, map[string][]byte{"art/deep/hero.bin": files["art/deep/hero.bin"]}, "art/deep/hero.bin")

		dir = cloneRepo(parent, repoURLFor(orgSlug, project), "several")
		for path, content := range files {
			editTail(dir, path, content)
		}
		restoreDirect(dir, files, "art/deep/hero.bin", "audio/music.bin", "top.bin", "art/icon.bin")

		dir = cloneRepo(parent, repoURLFor(orgSlug, project), "folder")
		editTail(dir, "art/deep/hero.bin", files["art/deep/hero.bin"])
		editTail(dir, "art/icon.bin", files["art/icon.bin"])
		restoreDirect(dir, map[string][]byte{
			"art/deep/hero.bin": files["art/deep/hero.bin"],
			"art/icon.bin":      files["art/icon.bin"],
		}, "art")
	})

	It("rebuilds a deleted binary without the object cache", func() {
		project := newProject("restore-direct-deleted")
		content := binaryAsset(6, 5)
		seedRepo(orgSlug, project, map[string][]byte{"assets/hero.bin": content}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")

		removePath(dir, "assets/hero.bin")
		restoreDirect(dir, map[string][]byte{"assets/hero.bin": content}, "assets/hero.bin")
	})

	It("restores text through the object cache", func() {
		project := newProject("restore-direct-text")
		seedRepo(orgSlug, project, map[string][]byte{"docs/notes.txt": []byte(strings.Repeat("committed line\n", 2000))}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		cached := objectEntries(dir)
		Expect(cachedObjects(dir)).NotTo(BeEmpty(), "text content is kept in the object cache")

		writeText(dir, "docs/notes.txt", "local edit\n")
		Expect(os.RemoveAll(filepath.Join(dir, ".nipa", "objects"))).To(Succeed())
		res := runNipa(dir, "restore", "docs/notes.txt")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(readText(dir, "docs/notes.txt")).To(Equal(strings.Repeat("committed line\n", 2000)))
		Expect(objectEntries(dir)).To(Equal(cached), "the text content went back into the cache, where the probe sees it")
		expectNoMaterializeTemps(dir)
		assertCleanStatus(dir)
	})
})
