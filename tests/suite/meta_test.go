package suite_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("nipa output modes", func() {
	It("honors NIPA_OUTPUT=json", func() {
		project := newProject("meta-json-env")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "b.txt", "b\n")
		Expect(runNipa(dir, "add", "b.txt").ExitCode).To(Equal(0))

		res := runNipaEnv(dir, []string{"NIPA_OUTPUT=json"}, "status")
		Expect(res.ExitCode).To(Equal(0), res.Output())

		var out statusJSON
		Expect(json.Unmarshal([]byte(res.Stdout), &out)).To(Succeed(), res.Stdout)
		Expect(out.Branch).To(Equal("main"))
		Expect(out.Staged).To(Equal([]string{"b.txt"}))
	})

	It("emits JSON error envelopes when JSON output is requested", func() {
		project := newProject("meta-json-error")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		res := runNipaEnv(dir, []string{"NIPA_OUTPUT=json"}, "push", "-m", "nothing")
		Expect(res.ExitCode).To(Equal(1))

		var envelope struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		Expect(json.Unmarshal([]byte(res.Stderr), &envelope)).To(Succeed(), res.Stderr)
		Expect(envelope.Error.Message).To(ContainSubstring("nothing staged"))
	})

	It("returns the push plan as JSON", func() {
		project := newProject("meta-push-plan")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")
		writeText(dir, "b.txt", "b\n")
		writeBytes(dir, "blob.bin", []byte{0x00, 0x01})
		Expect(runNipa(dir, "add", "b.txt", "blob.bin").ExitCode).To(Equal(0))

		var plan planJSON
		runJSONInto(dir, &plan, "push", "--dry-run", "--json", "-m", "preview")
		Expect(plan.Kind).To(Equal("push"))
		Expect(plan.Changes).To(HaveLen(2))
		statuses := map[string]string{}
		for _, change := range plan.Changes {
			statuses[change.Path] = change.Status
		}
		Expect(statuses["b.txt"]).To(Equal("A"))
		Expect(statuses["blob.bin"]).To(Equal("A"))
		Expect(plan.UploadObjects).To(BeNumerically(">", 0))
		Expect(plan.UploadBytes).To(BeNumerically(">", 0))
	})

	It("rejects unknown flags and commands", func() {
		project := newProject("meta-unknown")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(orgSlug, project), "work")

		flag := runNipa(dir, "update", "--json")
		Expect(flag.ExitCode).To(Equal(1))
		Expect(flag.Output()).NotTo(BeEmpty())

		command := runNipa(dir, "frobnicate")
		Expect(command.ExitCode).To(Equal(1))
		Expect(command.Output()).NotTo(BeEmpty())
	})
})
