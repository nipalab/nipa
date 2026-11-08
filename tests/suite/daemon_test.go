package suite_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

var _ = Describe("nipa serve", func() {
	It("publishes an endpoint and answers ping", func() {
		d := startDaemon()

		Expect(d.info.Port).To(BeNumerically(">", 0))
		Expect(d.info.Token).NotTo(BeEmpty())
		Expect(d.info.Version).NotTo(BeEmpty())
		Expect(d.info.PID).To(Equal(d.cmd.Process.Pid))

		ping, err := d.client.Ping(d.ctx, &daemonpb.PingRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(int(ping.GetPid())).To(Equal(d.info.PID))
		Expect(ping.GetVersion()).To(Equal(d.info.Version))
	})

	It("serves the JSON bridge for watch, status and push", func() {
		project := newProject("daemon-json")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		d := startDaemon()
		Expect(d.info.JSONPort).To(BeNumerically(">", 0))
		Expect(d.info.JSONPort).NotTo(Equal(d.info.Port))

		root, err := json.Marshal(map[string]string{"root": dir})
		Expect(err).NotTo(HaveOccurred())
		resp, _ := d.jsonPost("/v1/WatchRepo", string(root))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		writeText(dir, "b.txt", "from json\n")
		resp, body := d.jsonPost("/v1/Status", string(root))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var status struct {
			Branch    string   `json:"branch"`
			Untracked []string `json:"untracked"`
		}
		Expect(json.Unmarshal(body, &status)).To(Succeed())
		Expect(status.Branch).To(Equal("main"))
		Expect(status.Untracked).To(ContainElement("b.txt"))

		d.stage(dir, []string{"b.txt"}, nil)
		push, err := json.Marshal(map[string]string{"root": dir, "message": uniqueMessage("json push")})
		Expect(err).NotTo(HaveOccurred())
		resp, body = d.jsonPost("/v1/Push", string(push))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(Equal("application/x-ndjson"))
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		Expect(lines).NotTo(BeEmpty())
		Expect(lines[len(lines)-1]).To(ContainSubstring(`"result"`))

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "b.txt")).To(Equal("from json\n"))
	})

	It("reports Godot sidecar warnings over the JSON bridge", func() {
		project := newProject("daemon-godot-meta")
		importFile := func(uid string) []byte {
			return []byte("[remap]\n\nimporter=\"texture\"\ntype=\"CompressedTexture2D\"\nuid=\"" + uid + "\"\npath=\"res://.godot/imported/icon.png-0a1b2c3d.ctex\"\n\n[deps]\n\nsource_file=\"res://game/icon.png\"\n")
		}
		seedRepo(orgSlug, project, map[string][]byte{
			"game/project.godot":   []byte("config_version=5\n\n[application]\n\nconfig/name=\"Fixture\"\n"),
			"game/icon.png":        {0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00},
			"game/icon.png.import": importFile("uid://cvdk8a1nd4yxb"),
			"game/player.gd":       []byte("extends Node\n"),
			"game/player.gd.uid":   []byte("uid://bq4m1x7c2v8kd\n"),
		}, "godot fixture")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeBytes(dir, "game/icon.png.import", importFile("uid://b2q9m4x7c1v0z"))
		writeText(dir, "game/enemy.gd", "extends Node\n")

		d := startDaemon()
		root, err := json.Marshal(map[string]string{"root": dir})
		Expect(err).NotTo(HaveOccurred())
		resp, _ := d.jsonPost("/v1/WatchRepo", string(root))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		for _, engine := range []string{"godot", ""} {
			req, err := json.Marshal(map[string]string{"root": dir, "engine": engine})
			Expect(err).NotTo(HaveOccurred())
			resp, body := d.jsonPost("/v1/MetaWarnings", string(req))
			Expect(resp.StatusCode).To(Equal(http.StatusOK), string(body))
			var out struct {
				Warnings []struct {
					Path    string `json:"path"`
					Kind    string `json:"kind"`
					Related string `json:"related"`
				} `json:"warnings"`
			}
			Expect(json.Unmarshal(body, &out)).To(Succeed())
			kinds := map[string]string{}
			for _, w := range out.Warnings {
				kinds[w.Path] = w.Kind
			}
			Expect(kinds).To(Equal(map[string]string{
				"game/icon.png.import": "uid-changed",
				"game/enemy.gd":        "missing-sidecar",
			}), "engine %q", engine)
		}

		bad, err := json.Marshal(map[string]string{"root": dir, "engine": "unreal"})
		Expect(err).NotTo(HaveOccurred())
		resp, _ = d.jsonPost("/v1/MetaWarnings", string(bad))
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
	})

	It("watches a repository and lists it", func() {
		project := newProject("daemon-watch")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		d := startDaemon()
		repo := d.watch(dir)
		Expect(repo.GetRoot()).To(Equal(dir))
		Expect(repo.GetBranch()).To(Equal("main"))
		Expect(repo.GetUrl()).To(Equal(repoURLFor(orgSlug, project)))

		list, err := d.client.ListRepos(d.ctx, &daemonpb.ListReposRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(list.GetRepos()).To(HaveLen(1))
		Expect(list.GetRepos()[0].GetRoot()).To(Equal(dir))
	})

	It("reports status and stages changes through the daemon", func() {
		project := newProject("daemon-stage")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		d := startDaemon()
		d.watch(dir)
		writeText(dir, "b.txt", "b\n")

		st := d.status(dir)
		Expect(st.GetBranch()).To(Equal("main"))
		Expect(st.GetUntracked()).To(ContainElement("b.txt"))

		staged := d.stage(dir, []string{"b.txt"}, nil)
		Expect(staged.GetStaged()).To(ContainElement("b.txt"))

		cli := runNipa(dir, "status")
		Expect(cli.Output()).To(ContainSubstring("A  b.txt"))

		unstaged := d.stage(dir, nil, []string{"b.txt"})
		Expect(unstaged.GetStaged()).To(BeEmpty())

		cli = runNipa(dir, "status")
		Expect(cli.Output()).To(ContainSubstring("?  b.txt"))
		Expect(cli.Output()).NotTo(ContainSubstring("A  b.txt"))
	})

	It("streams working-copy changes and marks staged additions", func() {
		project := newProject("daemon-changes")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		d := startDaemon()
		d.watch(dir)
		stream, err := d.client.WatchChanges(d.ctx, &daemonpb.WatchChangesRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		_, err = stream.Header()
		Expect(err).NotTo(HaveOccurred())

		writeText(dir, "b.txt", "b\n")
		change, err := stream.Recv()
		Expect(err).NotTo(HaveOccurred())
		Expect(change.GetPaths()).To(ContainElement("b.txt"))

		writeText(dir, "a.txt", "edited\n")
		staged := d.stage(dir, []string{"a.txt", "b.txt"}, nil)
		Expect(staged.GetStaged()).To(ConsistOf("a.txt", "b.txt"))
		Expect(staged.GetAdded()).To(ConsistOf("b.txt"))
		Eventually(func() []string {
			change, err := stream.Recv()
			Expect(err).NotTo(HaveOccurred())
			return change.GetPaths()
		}).Should(BeEmpty(), "staging asks for a full refresh")
	})

	It("restores changes through the daemon", func() {
		project := newProject("daemon-restore")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		d := startDaemon()
		d.watch(dir)
		writeText(dir, "a.txt", "local edit\n")

		st := d.status(dir)
		Expect(st.GetModified()).To(ContainElement("a.txt"))

		restored := d.restore(dir, []string{"a.txt"})
		Expect(restored.GetModified()).To(BeEmpty())
		Expect(readText(dir, "a.txt")).To(Equal("v1\n"))
	})

	It("streams update results", func() {
		project := newProject("daemon-update")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		d := startDaemon()
		d.watch(dir)

		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v2\n")}, "remote update")

		stream, err := d.client.Update(d.ctx, &daemonpb.UpdateRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		events := collectOpEvents(stream)
		Expect(opFailure(events)).To(BeNil())
		result := opResult(events)
		Expect(result).NotTo(BeNil())
		Expect(result.GetSync()).NotTo(BeNil())
		Expect(result.GetSync().GetBranch()).To(Equal("main"))
		Expect(readText(dir, "a.txt")).To(Equal("v2\n"))
	})

	It("streams push results and lands the commit", func() {
		project := newProject("daemon-push")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		d := startDaemon()
		d.watch(dir)
		writeText(dir, "b.txt", "from the daemon\n")
		d.stage(dir, []string{"b.txt"}, nil)

		stream, err := d.client.Push(d.ctx, &daemonpb.PushRequest{Root: dir, Message: uniqueMessage("daemon push")})
		Expect(err).NotTo(HaveOccurred())
		events := collectOpEvents(stream)
		Expect(opFailure(events)).To(BeNil())
		result := opResult(events)
		Expect(result).NotTo(BeNil())
		Expect(result.GetPush()).NotTo(BeNil())
		Expect(result.GetPush().GetCommitId()).NotTo(BeEmpty())

		fresh := cloneRepo(parent, url, "fresh")
		Expect(readText(fresh, "b.txt")).To(Equal("from the daemon\n"))
	})

	It("switches branches and tags", func() {
		project := newProject("daemon-switch")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		d := startDaemon()
		d.watch(dir)

		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))

		stream, err := d.client.Switch(d.ctx, &daemonpb.SwitchRequest{Root: dir, Branch: "feature"})
		Expect(err).NotTo(HaveOccurred())
		events := collectOpEvents(stream)
		Expect(opFailure(events)).To(BeNil())
		Expect(opResult(events).GetSync()).NotTo(BeNil())
		Expect(readText(dir, "feature.txt")).To(Equal("feature\n"))

		Expect(runNipa(dir, "tag", "-c", "v1.0.0").ExitCode).To(Equal(0))
		tagStream, err := d.client.Switch(d.ctx, &daemonpb.SwitchRequest{Root: dir, Tag: "v1.0.0"})
		Expect(err).NotTo(HaveOccurred())
		tagEvents := collectOpEvents(tagStream)
		Expect(opFailure(tagEvents)).To(BeNil())

		st := d.status(dir)
		Expect(st.GetHead()).NotTo(BeNil())
		Expect(st.GetHead().GetKind()).To(Equal("tag"))
		Expect(st.GetHead().GetName()).To(Equal("v1.0.0"))
	})

	It("streams merge and revert results", func() {
		project := newProject("daemon-merge-revert")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		d := startDaemon()
		d.watch(dir)

		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))

		mergeStream, err := d.client.Merge(d.ctx, &daemonpb.MergeOpRequest{Root: dir, SourceBranch: "feature"})
		Expect(err).NotTo(HaveOccurred())
		mergeEvents := collectOpEvents(mergeStream)
		Expect(opFailure(mergeEvents)).To(BeNil())
		merge := opResult(mergeEvents).GetMerge()
		Expect(merge).NotTo(BeNil())
		Expect(merge.GetFastForwarded()).To(BeTrue())
		Expect(readText(dir, "feature.txt")).To(Equal("feature\n"))

		var log logJSON
		runJSONInto(dir, &log, "log", "--json")
		revertStream, err := d.client.Revert(d.ctx, &daemonpb.RevertOpRequest{Root: dir, Target: log.Commits[0].ID})
		Expect(err).NotTo(HaveOccurred())
		revertEvents := collectOpEvents(revertStream)
		Expect(opFailure(revertEvents)).To(BeNil())
		revert := opResult(revertEvents).GetRevert()
		Expect(revert).NotTo(BeNil())
		Expect(revert.GetCommitted()).To(BeTrue())
		Expect(fileExists(dir, "feature.txt")).To(BeFalse())
	})

	It("streams rendered diffs and failure events", func() {
		project := newProject("daemon-diff")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		d := startDaemon()
		d.watch(dir)
		writeText(dir, "a.txt", "v2\n")

		patchStream, err := d.client.Diff(d.ctx, &daemonpb.DiffRequest{Root: dir, Format: "patch"})
		Expect(err).NotTo(HaveOccurred())
		patch, failure := collectDiff(patchStream)
		Expect(failure).To(BeNil())
		Expect(patch).To(ContainSubstring("+v2"))

		statStream, err := d.client.Diff(d.ctx, &daemonpb.DiffRequest{Root: dir, Format: "stat"})
		Expect(err).NotTo(HaveOccurred())
		stat, failure := collectDiff(statStream)
		Expect(failure).To(BeNil())
		Expect(stat).To(ContainSubstring("1 file changed"))

		badStream, err := d.client.Diff(d.ctx, &daemonpb.DiffRequest{Root: dir, Revisions: []string{"no-such-revision"}})
		Expect(err).NotTo(HaveOccurred())
		_, failure = collectDiff(badStream)
		Expect(failure).NotTo(BeNil())
		Expect(failure.GetMessage()).NotTo(BeEmpty())
	})

	It("streams structured diffs with asset kinds", func() {
		project := newProject("daemon-diff-files")
		seedRepo(orgSlug, project, map[string][]byte{
			"a.txt":        []byte("v1\n"),
			"art/hero.png": testPNG(2),
		}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		d := startDaemon()
		d.watch(dir)
		writeText(dir, "a.txt", "v2\n")
		writeBytes(dir, "art/hero.png", testPNG(3))
		writeText(dir, "loc/en.csv", "key,value\n")
		Expect(runNipa(dir, "add", "loc/en.csv").ExitCode).To(Equal(0))

		stream, err := d.client.DiffFiles(d.ctx, &daemonpb.DiffRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		files, failure := collectDiffFiles(stream)
		Expect(failure).To(BeNil())

		byPath := map[string]*daemonpb.DiffFile{}
		for _, f := range files {
			byPath[f.GetPath()] = f
		}
		Expect(byPath).To(HaveLen(3))

		text := byPath["a.txt"]
		Expect(text.GetKind()).To(Equal("text"))
		Expect(text.GetStatus()).To(Equal("modified"))
		Expect(text.GetAdditions()).To(BeEquivalentTo(1))
		Expect(text.GetDeletions()).To(BeEquivalentTo(1))
		Expect(text.GetHunks()).NotTo(BeEmpty())

		png := byPath["art/hero.png"]
		Expect(png.GetKind()).To(Equal("image"))
		Expect(png.GetOld().GetIsBinary()).To(BeTrue())
		Expect(png.GetOld().GetHash()).NotTo(BeEmpty())
		Expect(png.GetNew().GetSizeBytes()).To(BeNumerically(">", 0))
		Expect(png.GetHunks()).To(BeEmpty())

		csv := byPath["loc/en.csv"]
		Expect(csv.GetKind()).To(Equal("table"))
		Expect(csv.GetStatus()).To(Equal("added"))
		Expect(csv.GetOld()).To(BeNil())

		badStream, err := d.client.DiffFiles(d.ctx, &daemonpb.DiffRequest{Root: dir, Revisions: []string{"no-such-revision"}})
		Expect(err).NotTo(HaveOccurred())
		_, failure = collectDiffFiles(badStream)
		Expect(failure).NotTo(BeNil())
	})

	It("reads blobs and previews image changes", func() {
		project := newProject("daemon-blob")
		seedRepo(orgSlug, project, map[string][]byte{
			"a.txt":        []byte("v1\n"),
			"art/hero.png": testPNG(2),
		}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		d := startDaemon()
		d.watch(dir)
		writeBytes(dir, "art/hero.png", testPNG(3))

		read := func(ref *daemonpb.BlobRef) (*daemonpb.BlobInfo, []byte, *daemonpb.OpFailure) {
			stream, err := d.client.ReadBlob(d.ctx, &daemonpb.ReadBlobRequest{Root: dir, Blob: ref})
			Expect(err).NotTo(HaveOccurred())
			return collectBlob(stream)
		}

		info, data, failure := read(&daemonpb.BlobRef{Path: "art/hero.png"})
		Expect(failure).To(BeNil())
		Expect(info.GetKind()).To(Equal("image"))
		Expect(data).To(Equal(testPNG(3)))

		info, data, failure = read(&daemonpb.BlobRef{Source: "snapshot", Path: "art/hero.png"})
		Expect(failure).To(BeNil())
		Expect(info.GetHash()).NotTo(BeEmpty())
		Expect(data).To(Equal(testPNG(2)))

		_, data, failure = read(&daemonpb.BlobRef{Source: "revision", Revision: "main", Path: "a.txt"})
		Expect(failure).To(BeNil())
		Expect(string(data)).To(Equal("v1\n"))

		_, _, failure = read(&daemonpb.BlobRef{Source: "revision", Revision: "main", Path: "missing.txt"})
		Expect(failure).NotTo(BeNil())
		Expect(failure.GetCode()).To(BeEquivalentTo(404))

		_, _, failure = read(&daemonpb.BlobRef{Path: "../outside"})
		Expect(failure.GetCode()).To(BeEquivalentTo(400))

		previewStream, err := d.client.Preview(d.ctx, &daemonpb.PreviewRequest{
			Root: dir,
			Old:  &daemonpb.BlobRef{Source: "snapshot", Path: "art/hero.png"},
			New:  &daemonpb.BlobRef{Path: "art/hero.png"},
		})
		Expect(err).NotTo(HaveOccurred())
		events, failure := collectPreview(previewStream)
		Expect(failure).To(BeNil())
		var stats *daemonpb.ImageDiffStats
		widths := map[string]int32{}
		for _, ev := range events {
			if img := ev.GetImage(); img != nil {
				widths[img.GetSide()] = img.GetWidth()
			}
			if ev.GetDiff() != nil {
				stats = ev.GetDiff()
			}
		}
		Expect(widths).To(Equal(map[string]int32{"old": 2, "new": 3}))
		Expect(stats.GetSizeChanged()).To(BeTrue())

		textPreview, err := d.client.Preview(d.ctx, &daemonpb.PreviewRequest{Root: dir, New: &daemonpb.BlobRef{Path: "a.txt"}})
		Expect(err).NotTo(HaveOccurred())
		_, failure = collectPreview(textPreview)
		Expect(failure.GetCode()).To(BeEquivalentTo(415))
	})

	It("proxies branch and lock calls with the clone context", func() {
		project := newProject("daemon-proxy")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		d := startDaemon()
		d.watch(dir)

		branches, err := d.client.ProxyBranchList(d.ctx, &daemonpb.ProxyBranchListRequest{
			Root:    dir,
			Request: &pb.GetListBranchRequest{Limit: 100},
		})
		Expect(err).NotTo(HaveOccurred())
		names := []string{}
		for _, branch := range branches.GetResponse().GetBranches() {
			names = append(names, branch.GetName())
		}
		Expect(names).To(ContainElement("main"))

		locked, err := d.client.ProxyLockFile(d.ctx, &daemonpb.ProxyLockFileRequest{
			Root:    dir,
			Request: &pb.LockFileRequest{Path: "assets/hero.png"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(locked.GetResponse().GetLock().GetPath()).To(Equal("assets/hero.png"))

		list, err := d.client.ProxyListFileLocks(d.ctx, &daemonpb.ProxyListFileLocksRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(list.GetResponse().GetLocks()).To(HaveLen(1))

		_, err = d.client.ProxyUnlockFile(d.ctx, &daemonpb.ProxyUnlockFileRequest{
			Root:    dir,
			Request: &pb.UnlockFileRequest{Path: "assets/hero.png"},
		})
		Expect(err).NotTo(HaveOccurred())

		after, err := d.client.ProxyListFileLocks(d.ctx, &daemonpb.ProxyListFileLocksRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(after.GetResponse().GetLocks()).To(BeEmpty())
	})

	It("reports the lock state of changed binaries", func() {
		project := newProject("daemon-lock-status")
		seedRepo(orgSlug, project, map[string][]byte{
			"a.txt":           []byte("a\n"),
			"assets/hero.png": {0x00, 0x01, 0x02},
			"assets/sky.psd":  {0x03, 0x04, 0x05},
		}, "seed")
		url := repoURLFor(orgSlug, project)
		writer := newIdentity("daemon-lock-writer").promote()
		writerDir := cloneRepo(workspace(), url, "writer")
		Expect(runNipaAs(writer, writerDir, "lock", "assets/sky.psd").ExitCode).To(Equal(0))

		dir := cloneRepo(workspace(), url, "work")
		writeText(dir, "assets/hero.png", "changed hero")
		writeText(dir, "assets/sky.psd", "changed sky")
		writeText(dir, "assets/new.fbx", "new mesh")
		writeText(dir, "a.txt", "b\n")

		d := startDaemon()
		d.watch(dir)
		locked, err := d.client.ProxyLockFile(d.ctx, &daemonpb.ProxyLockFileRequest{Root: dir, Request: &pb.LockFileRequest{Path: "assets/hero.png"}})
		Expect(err).NotTo(HaveOccurred())

		res, err := d.client.LockStatus(d.ctx, &daemonpb.LockStatusRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.GetUserId()).To(Equal(locked.GetResponse().GetLock().GetHeldBy()))
		Expect(res.GetBranch()).To(Equal("main"))
		Expect(res.GetDefaultBranch()).To(BeTrue())
		Expect(res.GetLocks()).To(HaveLen(2))
		states := map[string]string{}
		for _, f := range res.GetFiles() {
			states[f.GetPath()] = f.GetState()
		}
		Expect(states).To(Equal(map[string]string{
			"assets/hero.png": "mine",
			"assets/sky.psd":  "theirs",
			"assets/new.fbx":  "free",
		}))
	})

	It("signs in, browses a project and clones a sparse checkout", func() {
		project := newProject("daemon-clone")
		seedRepo(orgSlug, project, map[string][]byte{
			"readme.md":           []byte("hi\n"),
			"art/hero.png":        {0x00, 0x01},
			"art/props/crate.fbx": {0x02, 0x03},
			"audio/hit.wav":       {0x04, 0x05},
		}, "seed")

		d := startDaemonWithTokens(filepath.Join(GinkgoT().TempDir(), "tokens.json"))
		status, err := d.client.AccountStatus(d.ctx, &daemonpb.AccountStatusRequest{Server: cli.host})
		Expect(err).NotTo(HaveOccurred())
		Expect(status.GetSignedIn()).To(BeFalse())
		_, err = d.client.ListProjects(d.ctx, &daemonpb.ListProjectsRequest{Server: cli.host})
		Expect(err).To(HaveOccurred())

		_, err = d.client.Login(d.ctx, &daemonpb.LoginRequest{Host: cli.host, Username: cli.user, Password: "wrong-password"})
		Expect(err).To(HaveOccurred())
		_, err = d.client.Login(d.ctx, &daemonpb.LoginRequest{Host: cli.host, Username: cli.user, Password: cli.password})
		Expect(err).NotTo(HaveOccurred())
		status, err = d.client.AccountStatus(d.ctx, &daemonpb.AccountStatusRequest{Server: cli.host})
		Expect(err).NotTo(HaveOccurred())
		Expect(status.GetSignedIn()).To(BeTrue())
		Expect(status.GetEmail()).To(Equal(cli.user))
		Expect(status.GetUserId()).NotTo(BeEmpty())

		projects, err := d.client.ListProjects(d.ctx, &daemonpb.ListProjectsRequest{Server: cli.host})
		Expect(err).NotTo(HaveOccurred())
		found := false
		for _, p := range projects.GetProjects() {
			found = found || p.GetOrg() == orgSlug && p.GetProject() == project
		}
		Expect(found).To(BeTrue())

		branches, err := d.client.RemoteBranches(d.ctx, &daemonpb.RemoteBranchesRequest{Server: cli.host, Org: orgSlug, Project: project})
		Expect(err).NotTo(HaveOccurred())
		Expect(branches.GetBranches()).NotTo(BeEmpty())

		root, err := d.client.RemoteTree(d.ctx, &daemonpb.RemoteTreeRequest{Server: cli.host, Org: orgSlug, Project: project})
		Expect(err).NotTo(HaveOccurred())
		dirs := []string{}
		for _, dir := range root.GetDirs() {
			dirs = append(dirs, dir.GetPath())
		}
		Expect(dirs).To(Equal([]string{"art", "audio"}))
		Expect(root.GetFiles()).To(BeEquivalentTo(1))
		art, err := d.client.RemoteTree(d.ctx, &daemonpb.RemoteTreeRequest{Server: cli.host, Org: orgSlug, Project: project, Path: "art"})
		Expect(err).NotTo(HaveOccurred())
		Expect(art.GetDirs()).To(HaveLen(1))
		Expect(art.GetDirs()[0].GetPath()).To(Equal("art/props"))
		Expect(art.GetFiles()).To(BeEquivalentTo(1))

		target := filepath.Join(workspace(), "clone")
		stream, err := d.client.Clone(d.ctx, &daemonpb.CloneRequest{Url: repoURLFor(orgSlug, project), Sparse: []string{"art"}, Target: target})
		Expect(err).NotTo(HaveOccurred())
		last := lastOpEvent(stream)
		Expect(last.GetFailure()).To(BeNil(), "%v", last)
		Expect(last.GetResult().GetSync().GetBranch()).To(Equal("main"))
		Expect(fileExists(target, "art/props/crate.fbx")).To(BeTrue())
		Expect(fileExists(target, "audio/hit.wav")).To(BeFalse())

		info := d.watch(target)
		Expect(info.GetSparse()).To(Equal([]string{"art"}))
		sparse, err := d.client.SetSparse(d.ctx, &daemonpb.SetSparseRequest{Root: target, Paths: []string{"art", "audio"}})
		Expect(err).NotTo(HaveOccurred())
		last = lastOpEvent(sparse)
		Expect(last.GetFailure()).To(BeNil(), "%v", last)
		Expect(fileExists(target, "audio/hit.wav")).To(BeTrue())
		Expect(d.watch(target).GetSparse()).To(Equal([]string{"art", "audio"}))

		full, err := d.client.SetSparse(d.ctx, &daemonpb.SetSparseRequest{Root: target})
		Expect(err).NotTo(HaveOccurred())
		Expect(lastOpEvent(full).GetFailure()).To(BeNil())
		Expect(fileExists(target, "readme.md")).To(BeTrue())

		again, err := d.client.Clone(d.ctx, &daemonpb.CloneRequest{Url: repoURLFor(orgSlug, project), Target: target})
		Expect(err).NotTo(HaveOccurred())
		Expect(lastOpEvent(again).GetFailure()).NotTo(BeNil())
	})

	It("proxies merge request calls with the clone context", func() {
		project := newProject("daemon-mr-proxy")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "feature\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "mr", "create", "--title", "Proxied").ExitCode).To(Equal(0))

		d := startDaemon()
		d.watch(dir)

		list, err := d.client.ProxyMergeRequestList(d.ctx, &daemonpb.ProxyMergeRequestListRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(list.GetResponse().GetMergeRequests()).To(HaveLen(1))
		Expect(list.GetResponse().GetMergeRequests()[0].GetNumber()).To(Equal(int64(1)))

		got, err := d.client.ProxyMergeRequestGet(d.ctx, &daemonpb.ProxyMergeRequestGetRequest{
			Root:    dir,
			Request: &pb.GetMergeRequestRequest{Number: 1},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.GetResponse().GetMergeRequest().GetStatus()).To(Equal("open"))
		Expect(got.GetResponse().GetMergeability().GetStatus()).To(Equal("mergeable"))

		check, err := d.client.ProxyMergeRequestCheck(d.ctx, &daemonpb.ProxyMergeRequestCheckRequest{
			Root:    dir,
			Request: &pb.CheckMergeRequestRequest{Number: 1},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(check.GetResponse().GetMergeability().GetStatus()).To(Equal("mergeable"))

		review, err := d.client.ProxyMergeRequestSubmitReview(d.ctx, &daemonpb.ProxyMergeRequestSubmitReviewRequest{
			Root:    dir,
			Request: &pb.SubmitMergeRequestReviewRequest{Number: 1, State: "commented", Body: "noted"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(review.GetResponse().GetReview().GetState()).To(Equal("commented"))

		reviews, err := d.client.ProxyMergeRequestReviews(d.ctx, &daemonpb.ProxyMergeRequestReviewsRequest{
			Root:    dir,
			Request: &pb.ListMergeRequestReviewsRequest{Number: 1},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(reviews.GetResponse().GetReviews()).To(HaveLen(1))

		state, err := d.client.ProxyMergeRequestReviewState(d.ctx, &daemonpb.ProxyMergeRequestReviewStateRequest{
			Root:    dir,
			Request: &pb.GetMergeRequestReviewStateRequest{Number: 1},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(state.GetResponse().GetState()).NotTo(BeNil())

		threads, err := d.client.ProxyMergeRequestThreads(d.ctx, &daemonpb.ProxyMergeRequestThreadsRequest{
			Root:    dir,
			Request: &pb.ListMergeRequestThreadsRequest{Number: 1},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(threads.GetResponse().GetThreads()).To(BeEmpty())

		created, err := d.client.ProxyMergeRequestCreate(d.ctx, &daemonpb.ProxyMergeRequestCreateRequest{
			Root: dir,
			Request: &pb.CreateMergeRequestRequest{
				Title: "Second", SourceBranch: "feature", TargetBranch: "main",
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(created.GetResponse().GetMergeRequest().GetNumber()).To(Equal(int64(2)))

		closed, err := d.client.ProxyMergeRequestClose(d.ctx, &daemonpb.ProxyMergeRequestCloseRequest{
			Root:    dir,
			Request: &pb.CloseMergeRequestRequest{Number: 2},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(closed.GetResponse().GetMergeRequest().GetStatus()).To(Equal("closed"))

		reopened, err := d.client.ProxyMergeRequestReopen(d.ctx, &daemonpb.ProxyMergeRequestReopenRequest{
			Root:    dir,
			Request: &pb.ReopenMergeRequestRequest{Number: 2},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(reopened.GetResponse().GetMergeRequest().GetStatus()).To(Equal("open"))

		merged, err := d.client.ProxyMergeRequestMerge(d.ctx, &daemonpb.ProxyMergeRequestMergeRequest{
			Root:    dir,
			Request: &pb.MergeMergeRequestRequest{Number: 1, Strategy: "merge"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(merged.GetResponse().GetMergeRequest().GetStatus()).To(Equal("merged"))
	})

	It("browses history by path, shows commits, manages tags and resumes a revert", func() {
		project := newProject("daemon-history")
		seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n"), "art/hero.txt": []byte("v1\n")}, "seed")
		url := repoURLFor(orgSlug, project)
		dir := cloneRepo(workspace(), url, "work")

		d := startDaemon()
		d.watch(dir)

		writeText(dir, "art/hero.txt", "v2\n")
		Expect(runNipa(dir, "add", "art/hero.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "art pass").ExitCode).To(Equal(0))
		writeText(dir, "notes.txt", "notes\n")
		Expect(runNipa(dir, "add", "notes.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "notes").ExitCode).To(Equal(0))

		all, err := d.client.ProxyCommitLog(d.ctx, &daemonpb.ProxyCommitLogRequest{Root: dir, Request: &pb.GetCommitLogRequest{Limit: 10}})
		Expect(err).NotTo(HaveOccurred())
		Expect(all.GetResponse().GetCommits()).To(HaveLen(3))
		hero, err := d.client.ProxyCommitLog(d.ctx, &daemonpb.ProxyCommitLogRequest{Root: dir, Request: &pb.GetCommitLogRequest{Limit: 10, Path: "art/hero.txt"}})
		Expect(err).NotTo(HaveOccurred())
		heroCommits := hero.GetResponse().GetCommits()
		Expect(heroCommits).To(HaveLen(2))
		artPass, seed := heroCommits[0], heroCommits[1]
		Expect(artPass.GetMessage()).To(ContainSubstring("art pass"))
		Expect(seed.GetParent_1Id()).To(BeEmpty())

		showStream, err := d.client.DiffFiles(d.ctx, &daemonpb.DiffRequest{Root: dir, Revisions: []string{artPass.GetCommitId()}, Show: true})
		Expect(err).NotTo(HaveOccurred())
		shown, failure := collectDiffFiles(showStream)
		Expect(failure).To(BeNil())
		Expect(shown).To(HaveLen(1))
		Expect(shown[0].GetPath()).To(Equal("art/hero.txt"))
		Expect(shown[0].GetStatus()).To(Equal("modified"))

		rootStream, err := d.client.DiffFiles(d.ctx, &daemonpb.DiffRequest{Root: dir, Revisions: []string{seed.GetCommitId()}, Show: true})
		Expect(err).NotTo(HaveOccurred())
		rootFiles, failure := collectDiffFiles(rootStream)
		Expect(failure).To(BeNil())
		Expect(rootFiles).To(HaveLen(2))
		for _, f := range rootFiles {
			Expect(f.GetStatus()).To(Equal("added"))
		}

		created, err := d.client.ProxyTagCreate(d.ctx, &daemonpb.ProxyTagCreateRequest{Root: dir, Request: &pb.CreateTagRequest{Name: "art-v2", FromCommitId: artPass.GetCommitId(), Message: "approved"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(created.GetResponse().GetTag().GetCommitId()).To(Equal(artPass.GetCommitId()))
		tags, err := d.client.ProxyTagList(d.ctx, &daemonpb.ProxyTagListRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(tags.GetResponse().GetTags()).To(HaveLen(1))
		Expect(tags.GetResponse().GetTags()[0].GetMessage()).To(Equal("approved"))
		_, err = d.client.ProxyTagDelete(d.ctx, &daemonpb.ProxyTagDeleteRequest{Root: dir, Request: &pb.DeleteTagRequest{Name: "art-v2"}})
		Expect(err).NotTo(HaveOccurred())
		tags, err = d.client.ProxyTagList(d.ctx, &daemonpb.ProxyTagListRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(tags.GetResponse().GetTags()).To(BeEmpty())

		writeText(dir, "art/hero.txt", "v3\n")
		Expect(runNipa(dir, "add", "art/hero.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "art polish").ExitCode).To(Equal(0))

		revertStream, err := d.client.Revert(d.ctx, &daemonpb.RevertOpRequest{Root: dir, Target: artPass.GetCommitId()})
		Expect(err).NotTo(HaveOccurred())
		revertEvents := collectOpEvents(revertStream)
		Expect(opFailure(revertEvents)).To(BeNil())
		Expect(opResult(revertEvents).GetRevert().GetConflicts()).To(ConsistOf("art/hero.txt"))

		st := d.status(dir)
		Expect(st.GetPending()).To(Equal("revert"))
		Expect(st.GetPendingRemaining()).To(Equal(int32(1)))
		Expect(st.GetConflicts()).To(ConsistOf("art/hero.txt"))

		abortStream, err := d.client.Revert(d.ctx, &daemonpb.RevertOpRequest{Root: dir, Abort: true})
		Expect(err).NotTo(HaveOccurred())
		abortEvents := collectOpEvents(abortStream)
		Expect(opFailure(abortEvents)).To(BeNil())
		Expect(opResult(abortEvents).GetRevert().GetAborted()).To(BeTrue())
		st = d.status(dir)
		Expect(st.GetPending()).To(BeEmpty())
		Expect(st.GetConflicts()).To(BeEmpty())
		Expect(readText(dir, "art/hero.txt")).To(Equal("v3\n"))
	})

	It("drives a merge request conversation through the daemon", func() {
		project := newProject("daemon-mr-review")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "feature.txt", "one\ntwo\n")
		Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))

		d := startDaemon()
		d.watch(dir)

		created, err := d.client.ProxyMergeRequestCreate(d.ctx, &daemonpb.ProxyMergeRequestCreateRequest{Root: dir, Request: &pb.CreateMergeRequestRequest{Title: "Feature", SourceBranch: "feature", TargetBranch: "main", Draft: true}})
		Expect(err).NotTo(HaveOccurred())
		number := created.GetResponse().GetMergeRequest().GetNumber()
		Expect(created.GetResponse().GetMergeRequest().GetDraft()).To(BeTrue())

		ready := false
		updated, err := d.client.ProxyMergeRequestUpdate(d.ctx, &daemonpb.ProxyMergeRequestUpdateRequest{Root: dir, Request: &pb.UpdateMergeRequestRequest{Number: number, Description: "Adds feature.txt", Draft: &ready}})
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.GetResponse().GetMergeRequest().GetDraft()).To(BeFalse())
		Expect(updated.GetResponse().GetMergeRequest().GetDescription()).To(Equal("Adds feature.txt"))

		commits, err := d.client.ProxyMergeRequestCommits(d.ctx, &daemonpb.ProxyMergeRequestCommitsRequest{Root: dir, Request: &pb.ListMergeRequestCommitsRequest{Number: number}})
		Expect(err).NotTo(HaveOccurred())
		Expect(commits.GetResponse().GetCommits()).To(HaveLen(1))

		got, err := d.client.ProxyMergeRequestGet(d.ctx, &daemonpb.ProxyMergeRequestGetRequest{Root: dir, Request: &pb.GetMergeRequestRequest{Number: number}})
		Expect(err).NotTo(HaveOccurred())
		m := got.GetResponse().GetMergeability()
		filesStream, err := d.client.DiffFiles(d.ctx, &daemonpb.DiffRequest{Root: dir, Revisions: []string{m.GetMergeBaseCommitId(), m.GetSourceCommitId()}})
		Expect(err).NotTo(HaveOccurred())
		files, failure := collectDiffFiles(filesStream)
		Expect(failure).To(BeNil())
		Expect(files).To(HaveLen(1))
		Expect(files[0].GetPath()).To(Equal("feature.txt"))
		Expect(files[0].GetStatus()).To(Equal("added"))

		line := int64(2)
		inline, err := d.client.ProxyMergeRequestAddComment(d.ctx, &daemonpb.ProxyMergeRequestAddCommentRequest{Root: dir, Request: &pb.AddMergeRequestCommentRequest{Number: number, FilePath: "feature.txt", NewLine: &line, Body: "why two?"}})
		Expect(err).NotTo(HaveOccurred())
		threadID := inline.GetResponse().GetThread().GetId()
		Expect(inline.GetResponse().GetThread().GetNewLine()).To(Equal(int64(2)))
		_, err = d.client.ProxyMergeRequestAddComment(d.ctx, &daemonpb.ProxyMergeRequestAddCommentRequest{Root: dir, Request: &pb.AddMergeRequestCommentRequest{Number: number, Body: "overall fine"}})
		Expect(err).NotTo(HaveOccurred())

		reply, err := d.client.ProxyMergeRequestReply(d.ctx, &daemonpb.ProxyMergeRequestReplyRequest{Root: dir, Request: &pb.ReplyMergeRequestThreadRequest{Number: number, ThreadId: threadID, Body: "because"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(reply.GetResponse().GetComment().GetBody()).To(Equal("because"))
		resolved, err := d.client.ProxyMergeRequestResolve(d.ctx, &daemonpb.ProxyMergeRequestResolveRequest{Root: dir, Request: &pb.ResolveMergeRequestThreadRequest{Number: number, ThreadId: threadID, Resolved: true}})
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.GetResponse().GetThread().GetResolved()).To(BeTrue())

		threads, err := d.client.ProxyMergeRequestThreads(d.ctx, &daemonpb.ProxyMergeRequestThreadsRequest{Root: dir, Request: &pb.ListMergeRequestThreadsRequest{Number: number}})
		Expect(err).NotTo(HaveOccurred())
		Expect(threads.GetResponse().GetThreads()).To(HaveLen(2))

		Expect(runNipa(dir, "mr", "check", "1", "--name", "build", "--state", "success").ExitCode).To(Equal(0))
		checks, err := d.client.ProxyMergeRequestChecks(d.ctx, &daemonpb.ProxyMergeRequestChecksRequest{Root: dir, Request: &pb.ListMergeRequestChecksRequest{Number: number}})
		Expect(err).NotTo(HaveOccurred())
		Expect(checks.GetResponse().GetChecks()).To(HaveLen(1))
		Expect(checks.GetResponse().GetChecks()[0].GetState()).To(Equal("success"))

		timeline, err := d.client.ProxyMergeRequestTimeline(d.ctx, &daemonpb.ProxyMergeRequestTimelineRequest{Root: dir, Request: &pb.ListMergeRequestTimelineRequest{Number: number}})
		Expect(err).NotTo(HaveOccurred())
		kinds := []string{}
		for _, e := range timeline.GetResponse().GetItems() {
			kinds = append(kinds, e.GetKind())
		}
		Expect(kinds).To(ContainElements("opened", "ready_for_review"))
	})

	It("resolves text and binary merge conflicts through the daemon", func() {
		project := newProject("daemon-conflicts")
		seedRepo(orgSlug, project, map[string][]byte{"notes.txt": []byte("one\ntwo\nthree\n"), "art/hero.png": testPNG(1)}, "seed")
		url := repoURLFor(orgSlug, project)
		dir := cloneRepo(workspace(), url, "work")

		d := startDaemon()
		d.watch(dir)

		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		writeText(dir, "notes.txt", "one\nTWO feature\nthree\n")
		writeBytes(dir, "art/hero.png", testPNG(2))
		Expect(runNipa(dir, "lock", "art/hero.png").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "add", "notes.txt", "art/hero.png").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature edits").ExitCode).To(Equal(0))

		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		writeText(dir, "notes.txt", "one\nTWO main\nthree\n")
		writeBytes(dir, "art/hero.png", testPNG(3))
		Expect(runNipa(dir, "lock", "art/hero.png").ExitCode).To(Equal(0))
		Expect(runNipa(dir, "add", "notes.txt", "art/hero.png").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "main edits").ExitCode).To(Equal(0))

		mergeStream, err := d.client.Merge(d.ctx, &daemonpb.MergeOpRequest{Root: dir, SourceBranch: "feature"})
		Expect(err).NotTo(HaveOccurred())
		mergeEvents := collectOpEvents(mergeStream)
		Expect(opFailure(mergeEvents)).To(BeNil())
		Expect(opResult(mergeEvents).GetMerge().GetConflicts()).To(ConsistOf("notes.txt", "art/hero.png"))

		set, err := d.client.Conflicts(d.ctx, &daemonpb.ConflictsRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(set.GetOp()).To(Equal("merge"))
		Expect(set.GetOurs().GetLabel()).To(Equal("main"))
		Expect(set.GetTheirs().GetLabel()).To(Equal("feature"))
		kinds := map[string]*daemonpb.ConflictFile{}
		for _, f := range set.GetFiles() {
			kinds[f.GetPath()] = f
		}
		Expect(kinds["notes.txt"].GetKind()).To(Equal("text"))
		Expect(kinds["art/hero.png"].GetKind()).To(Equal("binary"))
		Expect(kinds["art/hero.png"].GetViewer()).To(Equal("image"))
		Expect(readText(dir, "notes.txt")).To(ContainSubstring("<<<<<<< ours"))

		theirsBlob, err := d.client.ReadBlob(d.ctx, &daemonpb.ReadBlobRequest{Root: dir, Blob: &daemonpb.BlobRef{Source: "revision", Revision: set.GetTheirs().GetId(), Path: "notes.txt"}})
		Expect(err).NotTo(HaveOccurred())
		_, theirsText, failure := collectBlob(theirsBlob)
		Expect(failure).To(BeNil())
		Expect(string(theirsText)).To(Equal("one\nTWO feature\nthree\n"))

		_, err = d.client.ResolveConflict(d.ctx, &daemonpb.ResolveConflictRequest{Root: dir, Path: "notes.txt", Choice: "working"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("conflict markers"))

		st, err := d.client.ResolveConflict(d.ctx, &daemonpb.ResolveConflictRequest{Root: dir, Path: "notes.txt", Choice: "content", Content: []byte("one\nTWO both\nthree\n")})
		Expect(err).NotTo(HaveOccurred())
		Expect(st.GetResolved()).To(ConsistOf("notes.txt"))
		st, err = d.client.ResolveConflict(d.ctx, &daemonpb.ResolveConflictRequest{Root: dir, Path: "art/hero.png", Choice: "theirs"})
		Expect(err).NotTo(HaveOccurred())
		Expect(st.GetResolved()).To(ConsistOf("notes.txt", "art/hero.png"))
		Expect(st.GetStaged()).To(ContainElements("notes.txt", "art/hero.png"))
		Expect(readBytes(dir, "art/hero.png")).To(Equal(testPNG(2)))

		Expect(runNipa(dir, "lock", "art/hero.png").ExitCode).To(Equal(0))
		pushStream, err := d.client.Push(d.ctx, &daemonpb.PushRequest{Root: dir, Message: "Merge branch 'feature' into 'main'"})
		Expect(err).NotTo(HaveOccurred())
		pushEvents := collectOpEvents(pushStream)
		Expect(opFailure(pushEvents)).To(BeNil())

		st = d.status(dir)
		Expect(st.GetPending()).To(BeEmpty())
		Expect(st.GetConflicts()).To(BeEmpty())
		empty, err := d.client.Conflicts(d.ctx, &daemonpb.ConflictsRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(empty.GetOp()).To(BeEmpty())

		check := cloneRepo(workspace(), url, "check")
		Expect(readText(check, "notes.txt")).To(Equal("one\nTWO both\nthree\n"))
		Expect(readBytes(check, "art/hero.png")).To(Equal(testPNG(2)))
	})

	It("merges a conflicted Godot scene over the JSON bridge", func() {
		base := "[gd_scene load_steps=2 format=3]\n\n[ext_resource type=\"Script\" path=\"res://level.gd\" id=\"1_lvl\"]\n\n[node name=\"Level\" type=\"Node2D\"]\nscript = ExtResource(\"1_lvl\")\n\n[node name=\"A\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(0, 0)\n\n[node name=\"B\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(0, 0)\n\n[node name=\"Camera\" type=\"Camera2D\" parent=\".\"]\n"
		project := newProject("daemon-godot-merge")
		seedRepo(orgSlug, project, map[string][]byte{"level.tscn": []byte(base)}, "seed")
		url := repoURLFor(orgSlug, project)
		dir := cloneRepo(workspace(), url, "work")

		d := startDaemon()
		d.watch(dir)

		Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
		feature := strings.Replace(base, "[node name=\"A\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(0, 0)", "[node name=\"A\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(16, 0)", 1)
		writeText(dir, "level.tscn", feature+"\n[node name=\"Coin\" type=\"Area2D\" parent=\".\"]\n")
		Expect(runNipa(dir, "add", "level.tscn").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "feature moves A").ExitCode).To(Equal(0))

		Expect(runNipa(dir, "switch", "main").ExitCode).To(Equal(0))
		main := strings.Replace(base, "[node name=\"B\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(0, 0)", "[node name=\"B\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(0, 32)", 1)
		writeText(dir, "level.tscn", main+"\n[node name=\"Door\" type=\"Area2D\" parent=\".\"]\n")
		Expect(runNipa(dir, "add", "level.tscn").ExitCode).To(Equal(0))
		Expect(pushRepo(dir, "main moves B").ExitCode).To(Equal(0))

		mergeStream, err := d.client.Merge(d.ctx, &daemonpb.MergeOpRequest{Root: dir, SourceBranch: "feature"})
		Expect(err).NotTo(HaveOccurred())
		mergeEvents := collectOpEvents(mergeStream)
		Expect(opFailure(mergeEvents)).To(BeNil())
		Expect(opResult(mergeEvents).GetMerge().GetConflicts()).To(ConsistOf("level.tscn"))

		req, err := json.Marshal(map[string]string{"root": dir, "path": "level.tscn"})
		Expect(err).NotTo(HaveOccurred())
		resp, body := d.jsonPost("/v1/MergeGodotResource", string(req))
		Expect(resp.StatusCode).To(Equal(http.StatusOK), string(body))
		var merged struct {
			Merged    []byte `json:"merged"`
			Clean     bool   `json:"clean"`
			Conflicts []any  `json:"conflicts"`
		}
		Expect(json.Unmarshal(body, &merged)).To(Succeed())
		Expect(merged.Clean).To(BeTrue(), string(body))
		Expect(merged.Conflicts).To(BeEmpty())
		text := string(merged.Merged)
		Expect(text).To(ContainSubstring("position = Vector2(16, 0)"))
		Expect(text).To(ContainSubstring("position = Vector2(0, 32)"))
		Expect(text).To(ContainSubstring(`[node name="Coin"`))
		Expect(text).To(ContainSubstring(`[node name="Door"`))
		Expect(text).NotTo(ContainSubstring("<<<<<<<"))
		Expect(readText(dir, "level.tscn")).To(ContainSubstring("<<<<<<<"), "the RPC only reads")

		resp, body = d.jsonPost("/v1/MergeGodotResource", `{"root":`+strconv.Quote(dir)+`,"path":"notes.txt"}`)
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest), string(body))

		resolve, err := json.Marshal(map[string]any{"root": dir, "path": "level.tscn", "choice": "content", "content": merged.Merged})
		Expect(err).NotTo(HaveOccurred())
		resp, body = d.jsonPost("/v1/ResolveConflict", string(resolve))
		Expect(resp.StatusCode).To(Equal(http.StatusOK), string(body))

		pushStream, err := d.client.Push(d.ctx, &daemonpb.PushRequest{Root: dir, Message: "Merge branch 'feature' into 'main'"})
		Expect(err).NotTo(HaveOccurred())
		Expect(opFailure(collectOpEvents(pushStream))).To(BeNil())
		Expect(d.status(dir).GetPending()).To(BeEmpty())

		check := cloneRepo(workspace(), url, "check")
		Expect(readText(check, "level.tscn")).To(Equal(text))
	})

	It("streams teammate pushes, released locks and review requests", func() {
		project := newProject("daemon-events")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n"), "assets/sky.psd": {0x01, 0x02}}, "seed")
		url := repoURLFor(orgSlug, project)
		mate := newIdentity("daemon-events-mate").promote()
		mateDir := cloneRepo(workspace(), url, "mate")
		Expect(runNipaAs(mate, mateDir, "lock", "assets/sky.psd").ExitCode).To(Equal(0))
		dir := cloneRepo(workspace(), url, "work")

		d := startDaemon()
		d.watch(dir)
		me, err := d.client.LockStatus(d.ctx, &daemonpb.LockStatusRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())

		stream, err := d.client.WatchEvents(d.ctx, &daemonpb.WatchEventsRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())
		activity := make(chan *daemonpb.RepoActivity, 64)
		go func() {
			defer GinkgoRecover()
			for {
				a, err := stream.Recv()
				if err != nil {
					close(activity)
					return
				}
				activity <- a
			}
		}()
		var first *daemonpb.RepoActivity
		Eventually(activity, "10s").Should(Receive(&first))
		Expect(first.GetEvents()).To(BeEmpty())
		Expect(first.GetError()).To(BeEmpty())
		Expect(first.GetIncoming()).To(BeZero())

		writeText(mateDir, "a.txt", "b\n")
		Expect(runNipaAs(mate, mateDir, "add", "a.txt").ExitCode).To(Equal(0))
		Expect(runNipaAs(mate, mateDir, "push", "-m", "Teammate edit").ExitCode).To(Equal(0))
		Expect(runNipaAs(mate, mateDir, "unlock", "assets/sky.psd").ExitCode).To(Equal(0))
		Expect(runNipaAs(mate, mateDir, "branch", "-c", "hero").ExitCode).To(Equal(0))
		writeText(mateDir, "hero.txt", "hero\n")
		Expect(runNipaAs(mate, mateDir, "add", "hero.txt").ExitCode).To(Equal(0))
		Expect(runNipaAs(mate, mateDir, "push", "-m", "Hero work").ExitCode).To(Equal(0))
		Expect(runNipaAs(mate, mateDir, "mr", "create", "--title", "Hero pass").ExitCode).To(Equal(0))
		Expect(runNipaAs(mate, mateDir, "mr", "request-review", "1", me.GetUserId()).ExitCode).To(Equal(0))

		seen := map[string]*daemonpb.TeamEvent{}
		var last *daemonpb.RepoActivity
		Eventually(func() int {
			for {
				select {
				case a, ok := <-activity:
					if !ok {
						return len(seen)
					}
					last = a
					for _, e := range a.GetEvents() {
						seen[e.GetKind()] = e
					}
				default:
					return len(seen)
				}
			}
		}, "30s", "200ms").Should(Equal(3))
		Expect(seen["pushed"].GetMessage()).To(Equal("Teammate edit"))
		Expect(seen["pushed"].GetBranch()).To(Equal("main"))
		Expect(seen["pushed"].GetCount()).To(Equal(int32(1)))
		Expect(seen["lock_released"].GetPath()).To(Equal("assets/sky.psd"))
		Expect(seen["review_requested"].GetNumber()).To(Equal(int64(1)))
		Expect(seen["review_requested"].GetMessage()).To(Equal("Hero pass"))
		Expect(last.GetIncoming()).To(Equal(int32(1)))
		Expect(last.GetReviewRequests()).To(Equal(int32(1)))
	})

	It("unwatches a repository", func() {
		project := newProject("daemon-unwatch")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		d := startDaemon()
		d.watch(dir)

		_, err := d.client.UnwatchRepo(d.ctx, &daemonpb.UnwatchRepoRequest{Root: dir})
		Expect(err).NotTo(HaveOccurred())

		_, err = d.client.Status(d.ctx, &daemonpb.StatusRequest{Root: dir})
		Expect(err).To(HaveOccurred())

		d.watch(dir)
		st := d.status(dir)
		Expect(st.GetBranch()).To(Equal("main"))
	})

	It("rejects missing or wrong daemon tokens", func() {
		d := startDaemon()

		_, err := d.client.Ping(contextWithoutToken(), &daemonpb.PingRequest{})
		Expect(err).To(HaveOccurred())

		wrong := metadataAppend(d.ctx, "wrong-token")
		_, err = d.client.Ping(wrong, &daemonpb.PingRequest{})
		Expect(err).To(HaveOccurred())
	})

	It("shuts down and removes the endpoint file", func() {
		d := startDaemon()
		_, err := d.client.Shutdown(d.ctx, &daemonpb.ShutdownRequest{})
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			_, err := os.Stat(d.endpoint)
			return os.IsNotExist(err)
		}, "10s", "100ms").Should(BeTrue())

		Eventually(d.done, "10s").Should(BeClosed())
	})
})
