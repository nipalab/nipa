package suite_test

import (
	"os"

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
