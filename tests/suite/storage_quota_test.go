package suite_test

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func storageLedgerEnabled() bool {
	if os.Getenv("NIPA_TEST_STORAGE_LEDGER") != "cloud" {
		Skip("storage ledger is not enabled")
	}
	return true
}

var _ = Describe("storage quota", func() {
	It("gates pushes on the owner quota and reports usage", func() {
		storageLedgerEnabled()

		if cli.edition == "free" {
			status, _ := api.getOrgStorage(orgSlug)
			Expect(status).To(Equal(http.StatusNotFound), "the free edition registers no org storage route")
			status, _ = api.doRaw(http.MethodGet, "/billing/me", nil)
			Expect(status).To(Equal(http.StatusNotFound), "the free edition registers no billing route")
			status, _ = api.setOwnerQuota("1", nil)
			Expect(status).To(Equal(http.StatusNotFound), "the free edition registers no admin quota route")
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

	It("recounts the ledger and guards the admin routes", func() {
		storageLedgerEnabled()

		if cli.edition == "free" {
			return
		}

		org := "quota-org-" + randSuffix(4)
		api.createOrg(org, org)
		project := "storage"
		api.createProject(org, project, project)

		parent := workspace()
		dir := cloneRepo(parent, repoURLFor(org, project), "work")
		writeText(dir, "data.txt", hex.EncodeToString(randomBytes(2048)))
		Expect(runNipa(dir, "add", "data.txt").ExitCode).To(Equal(0))
		push := pushRepo(dir, "add data")
		Expect(push.ExitCode).To(Equal(0), push.Output())

		status, storage := api.getOrgStorage(org)
		Expect(status).To(Equal(http.StatusOK))
		Expect(storage.UsedBytes).To(BeNumerically(">", 0))
		Expect(storage.OwnerID).To(Equal(api.me()))
		Expect(storage.Projects).To(HaveLen(1))
		Expect(storage.Projects[0].UsedBytes).To(Equal(storage.UsedBytes))

		status, recounted := api.recountOrgStorage(org)
		Expect(status).To(Equal(http.StatusOK))
		Expect(recounted.UsedBytes).To(Equal(storage.UsedBytes), "recount must reproduce the online usage")
		Expect(recounted.Projects).To(HaveLen(1))
		Expect(recounted.Projects[0].UsedBytes).To(Equal(storage.Projects[0].UsedBytes))

		suffix := fmt.Sprintf("storage-%d-%s", time.Now().UnixNano(), randSuffix(3))
		email := suffix + "@example.com"
		otherID := api.createUser("storage-user", email, "storage-password-123")
		other := newAPIClient(cli.apiURL, email, "storage-password-123")

		status, _ = other.setOwnerQuota(otherID, nil)
		Expect(status).To(Equal(http.StatusForbidden), "only super admins set quotas")
		status, _ = other.getOrgStorage(org)
		Expect(status).To(Equal(http.StatusForbidden), "non-members cannot read org storage")
		billing := other.getMyBilling()
		Expect(billing.UserID).To(Equal(otherID))

		status, moved := api.setOrgBillingOwner(org, otherID)
		Expect(status).To(Equal(http.StatusOK))
		Expect(moved.OwnerID).To(Equal(otherID))
		DeferCleanup(func() {
			status, _ := api.setOrgBillingOwner(org, api.me())
			Expect(status).To(Equal(http.StatusOK))
		})

		status, storage = api.getOrgStorage(org)
		Expect(status).To(Equal(http.StatusOK))
		Expect(storage.OwnerID).To(Equal(otherID), "usage moves with the organization")
		Expect(storage.UsedBytes).To(Equal(recounted.UsedBytes))
	})
})
