package suite_test

import (
	"encoding/hex"
	"net/http"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("storage quota", func() {
	It("gates pushes on the owner quota and reports usage", func() {
		if os.Getenv("NIPA_TEST_STORAGE_LEDGER") != "cloud" {
			Skip("storage ledger is not enabled")
		}

		if cli.edition == "free" {
			status, _ := api.getOrgStorage(orgSlug)
			Expect(status).To(Equal(http.StatusNotFound), "the free edition registers no storage routes")
			return
		}

		userID := api.me()
		Expect(userID).NotTo(BeEmpty())

		small := int64(64)
		status, _ := api.setOwnerQuota(userID, &small)
		Expect(status).To(Equal(http.StatusOK))
		DeferCleanup(func() {
			status, _ := api.setOwnerQuota(userID, nil)
			Expect(status).To(Equal(http.StatusOK))
		})

		project := newProject("quota")
		url := repoURLFor(orgSlug, project)
		parent := workspace()
		dir := cloneRepo(parent, url, "work")

		writeText(dir, "big.txt", hex.EncodeToString(randomBytes(4096)))
		Expect(runNipa(dir, "add", "big.txt").ExitCode).To(Equal(0))

		push := pushRepo(dir, "add big")
		Expect(push.ExitCode).NotTo(Equal(0), "push over quota must fail")
		Expect(push.Output()).To(ContainSubstring("quota exceeded"))

		bigger := int64(64 << 20)
		status, billing := api.setOwnerQuota(userID, &bigger)
		Expect(status).To(Equal(http.StatusOK))
		Expect(billing.QuotaBytes).NotTo(BeNil())
		Expect(*billing.QuotaBytes).To(Equal(bigger))

		push = pushRepo(dir, "add big")
		Expect(push.ExitCode).To(Equal(0), "push under the raised quota: %s", push.Output())

		status, storage := api.getOrgStorage(orgSlug)
		Expect(status).To(Equal(http.StatusOK))
		Expect(storage.UsedBytes).To(BeNumerically(">", 0))
		Expect(storage.QuotaBytes).NotTo(BeNil())
		Expect(*storage.QuotaBytes).To(Equal(bigger))
		Expect(storage.OwnerID).To(Equal(userID))

		found := false
		for _, p := range storage.Projects {
			if p.Slug == project {
				found = true
				Expect(p.UsedBytes).To(BeNumerically(">", 0))
			}
		}
		Expect(found).To(BeTrue(), "pushed project missing from the storage breakdown")

		billing = api.getMyBilling()
		Expect(billing.UserID).To(Equal(userID))
		Expect(billing.UsedBytes).To(BeNumerically(">", 0))
		Expect(billing.QuotaBytes).NotTo(BeNil())
	})
})
