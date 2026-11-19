package suite_test

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type importJSONResult struct {
	Source       string `json:"source"`
	Mirror       string `json:"mirror"`
	CacheRemoved bool   `json:"cache_removed"`
	LFSRead      int64  `json:"lfs_read"`
	Commits      int    `json:"commits"`
	Known        int    `json:"known"`
	Imported     int    `json:"imported"`
	Branches     int    `json:"branches"`
	Tags         int    `json:"tags"`
	Refs         []struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"refs"`
	RefWarnings []struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"ref_warnings"`
	Report struct {
		Commits         int   `json:"commits"`
		Blobs           int   `json:"blobs"`
		Bytes           int64 `json:"bytes"`
		LFSObjects      int   `json:"lfs_objects"`
		LFSMissingCount int   `json:"lfs_missing_count"`
	} `json:"report"`
}

func (r importJSONResult) status(kind, name string) string {
	for _, ref := range r.Refs {
		if ref.Kind == kind && ref.Name == name {
			return ref.Status
		}
	}
	return ""
}

type gitAuthor struct {
	name  string
	email string
	when  time.Time
}

// gitSource builds a Git repository with the git binary when it is
// installed and with go-git otherwise (or when NIPA_TEST_GOGIT is set).
type gitSource struct {
	dir  string
	cli  bool
	repo *git.Repository
}

func newGitSource() *gitSource {
	GinkgoHelper()
	dir := filepath.Join(workspace(), "git-src")
	g := &gitSource{dir: dir}
	if _, err := exec.LookPath("git"); err == nil && os.Getenv("NIPA_TEST_GOGIT") == "" {
		g.cli = true
		Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
		g.git(gitAuthor{}, "init", "-q", "-b", "main")
		return g
	}
	repo, err := git.PlainInit(dir, false)
	Expect(err).NotTo(HaveOccurred())
	Expect(repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main")))).To(Succeed())
	g.repo = repo
	return g
}

func (g *gitSource) git(a gitAuthor, args ...string) string {
	GinkgoHelper()
	cmd := exec.Command("git", args...)
	cmd.Dir = g.dir
	date := a.when.Format(time.RFC3339)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME="+a.name, "GIT_AUTHOR_EMAIL="+a.email, "GIT_AUTHOR_DATE="+date,
		"GIT_COMMITTER_NAME="+a.name, "GIT_COMMITTER_EMAIL="+a.email, "GIT_COMMITTER_DATE="+date,
	)
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

func (g *gitSource) write(files map[string][]byte, exec ...string) {
	GinkgoHelper()
	for path, data := range files {
		writeBytes(g.dir, path, data)
	}
	for _, path := range exec {
		Expect(os.Chmod(filepath.Join(g.dir, path), 0o755)).To(Succeed())
	}
}

func (g *gitSource) commit(a gitAuthor, message string, files map[string][]byte, exec ...string) {
	GinkgoHelper()
	g.write(files, exec...)
	if g.cli {
		g.git(a, "add", "-A")
		g.git(a, "commit", "-q", "-m", message)
		return
	}
	g.goCommit(a, message)
}

func (g *gitSource) goCommit(a gitAuthor, message string, parents ...plumbing.Hash) {
	GinkgoHelper()
	wt, err := g.repo.Worktree()
	Expect(err).NotTo(HaveOccurred())
	Expect(wt.AddWithOptions(&git.AddOptions{All: true})).To(Succeed())
	sig := &object.Signature{Name: a.name, Email: a.email, When: a.when}
	opts := &git.CommitOptions{Author: sig, Committer: sig, AllowEmptyCommits: true}
	if len(parents) > 0 {
		head, err := g.repo.Head()
		Expect(err).NotTo(HaveOccurred())
		opts.Parents = append([]plumbing.Hash{head.Hash()}, parents...)
	}
	_, err = wt.Commit(message, opts)
	Expect(err).NotTo(HaveOccurred())
}

func (g *gitSource) checkout(branch string, create bool) {
	GinkgoHelper()
	if g.cli {
		if create {
			g.git(gitAuthor{}, "checkout", "-q", "-b", branch)
		} else {
			g.git(gitAuthor{}, "checkout", "-q", branch)
		}
		return
	}
	wt, err := g.repo.Worktree()
	Expect(err).NotTo(HaveOccurred())
	Expect(wt.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(branch), Create: create, Keep: create})).To(Succeed())
}

// merge merges branch into the checked-out branch; branchFiles are the
// files the branch added, written by hand for go-git.
func (g *gitSource) merge(a gitAuthor, branch, message string, branchFiles map[string][]byte) {
	GinkgoHelper()
	if g.cli {
		g.git(a, "merge", "-q", "--no-ff", "-m", message, branch)
		return
	}
	ref, err := g.repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	Expect(err).NotTo(HaveOccurred())
	g.write(branchFiles)
	g.goCommit(a, message, ref.Hash())
}

func (g *gitSource) tag(a gitAuthor, name, message, target string) {
	GinkgoHelper()
	if g.cli {
		g.git(a, "tag", "-a", name, "-m", message, target)
		return
	}
	ref, err := g.repo.Reference(plumbing.NewBranchReferenceName(target), true)
	Expect(err).NotTo(HaveOccurred())
	_, err = g.repo.CreateTag(name, ref.Hash(), &git.CreateTagOptions{Tagger: &object.Signature{Name: a.name, Email: a.email, When: a.when}, Message: message})
	Expect(err).NotTo(HaveOccurred())
}

// files returns the content of a ref: git archive when git is installed,
// else the go-git tree.
func (g *gitSource) files(ref string) map[string][]byte {
	GinkgoHelper()
	out := map[string][]byte{}
	if g.cli {
		cmd := exec.Command("git", "archive", "--format=tar", ref)
		cmd.Dir = g.dir
		data, err := cmd.Output()
		Expect(err).NotTo(HaveOccurred())
		tr := tar.NewReader(bytes.NewReader(data))
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			Expect(err).NotTo(HaveOccurred())
			if hdr.Typeflag != tar.TypeReg {
				continue
			}
			content, err := io.ReadAll(tr)
			Expect(err).NotTo(HaveOccurred())
			out[hdr.Name] = content
		}
		return out
	}
	h, err := g.repo.ResolveRevision(plumbing.Revision(ref))
	Expect(err).NotTo(HaveOccurred())
	c, err := g.repo.CommitObject(*h)
	Expect(err).NotTo(HaveOccurred())
	iter, err := c.Files()
	Expect(err).NotTo(HaveOccurred())
	Expect(iter.ForEach(func(f *object.File) error {
		content, err := f.Contents()
		out[f.Name] = []byte(content)
		return err
	})).To(Succeed())
	return out
}

func workingFiles(dir string) map[string][]byte {
	GinkgoHelper()
	out := map[string][]byte{}
	Expect(filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			if rel == ".nipa" {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		out[filepath.ToSlash(rel)] = data
		return err
	})).To(Succeed())
	return out
}

func runImport(args ...string) (importJSONResult, commandResult) {
	GinkgoHelper()
	var out importJSONResult
	res := runJSONInto(workspace(), &out, append([]string{"import", "git"}, args...)...)
	return out, res
}

func runImportEnv(env []string, args ...string) (importJSONResult, commandResult) {
	GinkgoHelper()
	var out importJSONResult
	res := runNipaEnv(workspace(), env, append([]string{"import", "git"}, args...)...)
	Expect(res.ExitCode).To(Equal(0), res.Output())
	Expect(json.Unmarshal([]byte(res.Stdout), &out)).To(Succeed(), "stdout: %s", res.Stdout)
	return out, res
}

func lfsPointer(gitDir string, content []byte) []byte {
	GinkgoHelper()
	sum := sha256.Sum256(content)
	oid := hex.EncodeToString(sum[:])
	writeBytes(filepath.Join(gitDir, "lfs", "objects", oid[:2], oid[2:4]), oid, content)
	return []byte(fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", oid, len(content)))
}

var _ = Describe("nipa import git", func() {
	ann := gitAuthor{name: "Ann Author", email: "ann@example.com", when: time.Date(2019, 5, 6, 7, 8, 9, 0, time.UTC)}
	bob := gitAuthor{name: "Bob Builder", email: "bob@example.com", when: time.Date(2019, 6, 7, 8, 9, 10, 0, time.UTC)}
	cid := gitAuthor{name: "Cid", email: "cid@example.com", when: time.Date(2019, 7, 8, 9, 10, 11, 0, time.UTC)}

	It("imports history, branches and tags, then mirrors with --update", func() {
		project := newProject("import-git")
		url := repoURLFor(orgSlug, project)
		src := newGitSource()
		hero := randomBytes(300 << 10)
		src.commit(ann, "first", map[string][]byte{"README.md": []byte("# game\n"), "art/hero.png": hero, "tools/build.sh": []byte("#!/bin/sh\nmake\n")}, "tools/build.sh")
		src.commit(bob, "second", map[string][]byte{"README.md": []byte("# game\nnow with docs\n")})
		src.tag(bob, "v1", "first release", "main")
		src.checkout("feature", true)
		src.commit(ann, "feature work", map[string][]byte{"docs/feature.md": []byte("feature\n")})
		src.checkout("main", false)
		src.merge(cid, "feature", "merge feature", map[string][]byte{"docs/feature.md": []byte("feature\n")})

		dry, res := runImport(src.dir, url, "--dry-run", "--json")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(dry.Commits).To(Equal(4))
		Expect(dry.Branches).To(Equal(2))
		Expect(dry.Tags).To(Equal(1))
		Expect(dry.Report.Blobs).To(BeNumerically(">=", 5))
		Expect(dry.Refs).To(BeEmpty())

		refused := runNipa(workspace(), "import", "git", src.dir, url)
		Expect(refused.ExitCode).To(Equal(1), refused.Output())
		Expect(refused.Output()).To(ContainSubstring("--yes"))

		text := runNipa(workspace(), "import", "git", src.dir, url, "--yes")
		Expect(text.ExitCode).To(Equal(0), text.Output())
		Expect(text.Output()).To(ContainSubstring("Imported 4 commits"))

		for _, branch := range []string{"main", "feature"} {
			dir := cloneRepo(workspace(), url, "clone-"+branch, "--branch", branch)
			Expect(workingFiles(dir)).To(Equal(src.files(branch)), "branch %s matches git", branch)
		}
		main := cloneRepo(workspace(), url, "main-log")
		info, err := os.Stat(filepath.Join(main, "tools/build.sh"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm() & 0o100).NotTo(BeZero())

		var log logJSON
		runJSONInto(main, &log, "log", "--json")
		Expect(log.Commits).To(HaveLen(3), "nipa log follows first parents")
		byMessage := map[string]int{}
		for i, c := range log.Commits {
			byMessage[strings.TrimSpace(c.Message)] = i
		}
		first := log.Commits[byMessage["first"]]
		Expect(first.AuthorName).To(Equal(ann.name))
		Expect(first.AuthorEmail).To(Equal(ann.email))
		Expect(first.CreatedAt).To(Equal(ann.when.Format(time.RFC3339)))
		merge := log.Commits[byMessage["merge feature"]]
		Expect(merge.AuthorName).To(Equal(cid.name))
		Expect(merge.CreatedAt).To(Equal(cid.when.Format(time.RFC3339)))
		Expect(merge.ParentIDs).To(HaveLen(2))

		var tags tagsJSON
		runJSONInto(main, &tags, "tag", "--json")
		Expect(tags.Tags).To(HaveLen(1))
		Expect(tags.Tags[0].Name).To(Equal("v1"))
		Expect(tags.Tags[0].Message).To(ContainSubstring("first release"))

		again, res := runImport(src.dir, url, "--yes", "--json")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(again.Commits).To(Equal(0))
		Expect(again.status("branch", "main")).To(Equal("unchanged"))

		later := gitAuthor{name: "Dee", email: "dee@example.com", when: time.Date(2019, 8, 1, 0, 0, 0, 0, time.UTC)}
		src.commit(later, "after cutover", map[string][]byte{"notes.txt": []byte("more\n")})
		up, res := runImport(src.dir, url, "--update", "--yes", "--json")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(up.Commits).To(Equal(1))
		Expect(up.Imported).To(Equal(1))
		Expect(up.status("branch", "main")).To(Equal("updated"))
		Expect(up.RefWarnings).To(BeEmpty())
		Expect(runNipa(main, "update").ExitCode).To(Equal(0))
		Expect(workingFiles(main)).To(Equal(src.files("main")))

		writeText(main, "nipa-only.txt", "pushed in nipa\n")
		Expect(runNipa(main, "add", "nipa-only.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(main, uniqueMessage("nipa change")).ExitCode).To(Equal(0))
		src.commit(later, "git keeps going", map[string][]byte{"notes.txt": []byte("even more\n")})
		div, res := runImport(src.dir, url, "--update", "--yes", "--json")
		Expect(res.ExitCode).To(Equal(0), res.Output())
		Expect(div.Imported).To(Equal(1))
		Expect(div.status("branch", "main")).To(Equal("diverged"))
		Expect(div.RefWarnings).To(HaveLen(1))
		Expect(div.RefWarnings[0].Name).To(Equal("main"))
		Expect(runNipa(main, "update").ExitCode).To(Equal(0))
		Expect(readText(main, "nipa-only.txt")).To(Equal("pushed in nipa\n"))

		textWarn := runNipa(workspace(), "import", "git", src.dir, url, "--update", "--yes")
		Expect(textWarn.ExitCode).To(Equal(0), textWarn.Output())
		Expect(textWarn.Output()).To(ContainSubstring("warning: branch main diverged"))
	})

	It("refuses an importer who is not a project admin", func() {
		project := newProject("import-git-admin")
		url := repoURLFor(orgSlug, project)
		src := newGitSource()
		src.commit(ann, "first", map[string][]byte{"a.txt": []byte("a\n")})
		Expect(runNipa(workspace(), "import", "git", src.dir, url, "--yes").ExitCode).To(Equal(0))

		writer := newIdentity("import-writer")
		admin := cloneRepo(workspace(), url, "admin")
		grantRule(admin, "--user", writer.userID, "--permission", "read,write")

		src.commit(bob, "second", map[string][]byte{"b.txt": []byte("b\n")})
		res := runNipaAs(writer, workspace(), "import", "git", src.dir, url, "--update", "--yes")
		Expect(res.ExitCode).NotTo(Equal(0), res.Output())
		Expect(strings.ToLower(res.Output())).To(ContainSubstring("permission"))

		var log logJSON
		runJSONInto(admin, &log, "log", "--json")
		Expect(log.Commits).To(HaveLen(1))
	})

	It("imports a file:// URL through a cached mirror and resolves LFS objects", func() {
		project := newProject("import-git-url")
		url := repoURLFor(orgSlug, project)
		src := newGitSource()
		hero := randomBytes(200 << 10)
		src.commit(ann, "first", map[string][]byte{"README.md": []byte("# lfs game\n"), "art/hero.png": lfsPointer(filepath.Join(src.dir, ".git"), hero)})
		remote := "file://" + filepath.ToSlash(src.dir)
		env := []string{"XDG_CACHE_HOME=" + workspace()}

		dry, _ := runImportEnv(env, remote, url, "--dry-run", "--json", "--lfs", "local")
		Expect(dry.Source).To(Equal(remote))
		Expect(dry.Report.LFSObjects).To(Equal(1))
		Expect(dry.Report.LFSMissingCount).To(Equal(0))
		Expect(dry.Report.Bytes).To(BeNumerically(">=", len(hero)))

		first, _ := runImportEnv(env, remote, url, "--yes", "--json")
		Expect(first.Imported).To(Equal(1))
		Expect(first.LFSRead).To(Equal(int64(1)))
		Expect(first.Mirror).NotTo(BeEmpty())
		Expect(filepath.Join(first.Mirror, "nipa-import", "state.db")).To(BeAnExistingFile())

		main := cloneRepo(workspace(), url, "lfs-main")
		files := workingFiles(main)
		Expect(files["art/hero.png"]).To(Equal(hero), "the lfs object, not the pointer, is imported")

		src.commit(bob, "second", map[string][]byte{"notes.txt": []byte("fetched\n")})
		up, _ := runImportEnv(env, remote, url, "--update", "--yes", "--json")
		Expect(up.Imported).To(Equal(1), "the mirror fetches the new commit")
		Expect(up.status("branch", "main")).To(Equal("updated"))
		Expect(up.Mirror).To(Equal(first.Mirror))

		last, _ := runImportEnv(env, remote, url, "--update", "--yes", "--json", "--keep-cache=false")
		Expect(last.Commits).To(Equal(0))
		Expect(last.CacheRemoved).To(BeTrue())
		Expect(last.Mirror).NotTo(BeADirectory())

		missing := runNipaEnv(workspace(), env, "import", "git", "file://"+filepath.ToSlash(filepath.Join(workspace(), "none")), url, "--yes")
		Expect(missing.ExitCode).To(Equal(1), missing.Output())
		Expect(missing.Output()).To(ContainSubstring("not found"))
	})
})
