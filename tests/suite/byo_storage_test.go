package suite_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type storageServerJSON struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type storageServerListJSON struct {
	Servers []storageServerJSON `json:"servers"`
}

type storageServerSummaryJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type orgStorageServerJSON struct {
	Server    *storageServerSummaryJSON `json:"server"`
	CanManage bool                      `json:"can_manage"`
}

type projectStorageJSON struct {
	External bool                      `json:"external"`
	Server   *storageServerSummaryJSON `json:"server"`
}

type probeErrorJSON struct {
	Error string `json:"error"`
	Check string `json:"check"`
}

type byoTarget struct {
	endpoint  string
	bucket    string
	accessKey string
	secretKey string
}

// byoStorage returns the second MinIO bucket run.sh prepares for the
// bring-your-own storage specs; it skips when the server runs without them.
func byoStorage() byoTarget {
	GinkgoHelper()
	if os.Getenv("NIPA_TEST_BYO") != "1" {
		Skip("bring-your-own storage is not enabled (ee with NIPA_TEST_SAAS and the storage ledger)")
	}
	return byoTarget{
		endpoint:  os.Getenv("NIPA_TEST_BYO_S3_ENDPOINT"),
		bucket:    os.Getenv("NIPA_TEST_BYO_S3_BUCKET"),
		accessKey: os.Getenv("NIPA_TEST_BYO_S3_ACCESS_KEY"),
		secretKey: os.Getenv("NIPA_TEST_BYO_S3_SECRET_KEY"),
	}
}

func (t byoTarget) request(name, prefix string) map[string]string {
	return map[string]string{
		"name":              name,
		"endpoint":          t.endpoint,
		"region":            "us-east-1",
		"bucket":            t.bucket,
		"prefix":            prefix,
		"access_key_id":     t.accessKey,
		"secret_access_key": t.secretKey,
	}
}

// objectsUnder lists the object keys below prefix in the bucket, read with
// the MinIO client independently of the server.
func (t byoTarget) objectsUnder(prefix string) []string {
	GinkgoHelper()
	host := strings.TrimPrefix(strings.TrimPrefix(t.endpoint, "http://"), "https://")
	client, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(t.accessKey, t.secretKey, ""),
		Secure: strings.HasPrefix(t.endpoint, "https://"),
	})
	Expect(err).NotTo(HaveOccurred())
	var keys []string
	for obj := range client.ListObjects(context.Background(), t.bucket, minio.ListObjectsOptions{Prefix: prefix + "/", Recursive: true}) {
		Expect(obj.Err).NotTo(HaveOccurred())
		keys = append(keys, obj.Key)
	}
	return keys
}

func decodeInto(body []byte, out any) {
	GinkgoHelper()
	Expect(json.Unmarshal(body, out)).To(Succeed(), "%s", body)
}

var _ = Describe("bring-your-own storage servers", func() {
	It("registers, probes, edits and deletes the caller's storage servers", func() {
		if cli.edition == "free" {
			routes := []struct{ method, path string }{
				{http.MethodGet, "/me/storage-servers"},
				{http.MethodPost, "/me/storage-servers"},
				{http.MethodGet, "/me/storage-servers/1"},
				{http.MethodPatch, "/me/storage-servers/1"},
				{http.MethodDelete, "/me/storage-servers/1"},
				{http.MethodPost, "/me/storage-servers/1/test"},
				{http.MethodGet, "/orgs/" + orgSlug + "/storage-server"},
				{http.MethodPut, "/orgs/" + orgSlug + "/storage-server"},
				{http.MethodDelete, "/orgs/" + orgSlug + "/storage-server"},
			}
			for _, route := range routes {
				status, body := api.doRaw(route.method, route.path, nil)
				Expect(status).To(Equal(http.StatusNotFound), "%s %s: %s", route.method, route.path, body)
			}
			// The project storage route falls through to the project resolver,
			// which answers 404 for the unknown sub-path too.
			project := newProject("byo-free")
			status, body := api.doRaw(http.MethodGet, "/orgs/"+orgSlug+"/projects/"+project+"/storage", nil)
			Expect(status).To(Equal(http.StatusNotFound), "%s", body)
			return
		}
		target := byoStorage()
		owner := newIdentity("byo-owner")
		ownerAPI := newAPIClient(cli.apiURL, owner.email, owner.password)
		prefix := "e2e-" + randSuffix(4)

		var list storageServerListJSON
		status, body := ownerAPI.doRaw(http.MethodGet, "/me/storage-servers", nil)
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		decodeInto(body, &list)
		Expect(list.Servers).To(BeEmpty())

		invalid := func(mutate func(map[string]string), substr string) {
			GinkgoHelper()
			req := target.request("broken", prefix)
			mutate(req)
			status, body := ownerAPI.doRaw(http.MethodPost, "/me/storage-servers", req)
			Expect(status).To(Equal(http.StatusBadRequest), "%s", body)
			Expect(string(body)).To(ContainSubstring(substr))
		}
		invalid(func(r map[string]string) { r["name"] = " " }, "name is required")
		invalid(func(r map[string]string) { r["access_key_id"] = "" }, "access_key_id is required")
		invalid(func(r map[string]string) { r["secret_access_key"] = "" }, "secret_access_key is required")

		// A failed probe names the check that failed and stores nothing.
		req := target.request("wrong-secret", prefix)
		req["secret_access_key"] = "not-the-secret"
		status, body = ownerAPI.doRaw(http.MethodPost, "/me/storage-servers", req)
		Expect(status).To(Equal(http.StatusBadRequest), "%s", body)
		var probe probeErrorJSON
		decodeInto(body, &probe)
		Expect(probe.Check).NotTo(BeEmpty(), "the failing probe check is named")
		Expect(probe.Error).NotTo(BeEmpty())
		req = target.request("missing-bucket", prefix)
		req["bucket"] = "nipa-missing-" + randSuffix(4)
		status, body = ownerAPI.doRaw(http.MethodPost, "/me/storage-servers", req)
		Expect(status).To(Equal(http.StatusBadRequest), "%s", body)
		decodeInto(body, &probe)
		Expect(probe.Check).To(Equal("bucket"))

		status, body = ownerAPI.doRaw(http.MethodPost, "/me/storage-servers", target.request("studio bucket", prefix))
		Expect(status).To(Equal(http.StatusCreated), "%s", body)
		var server storageServerJSON
		decodeInto(body, &server)
		Expect(server.ID).NotTo(BeEmpty())
		Expect(server.Name).To(Equal("studio bucket"))
		Expect(server.Bucket).To(Equal(target.bucket))
		Expect(server.Prefix).To(Equal(prefix))
		Expect(server.AccessKeyID).To(Equal(target.accessKey))
		Expect(server.SecretAccessKey).To(BeEmpty(), "the secret is write-only")
		Expect(string(body)).NotTo(ContainSubstring("secret_access_key"))

		status, body = ownerAPI.doRaw(http.MethodPost, "/me/storage-servers", target.request("studio bucket", prefix+"-other"))
		Expect(status).To(Equal(http.StatusConflict), "names are unique per owner: %s", body)

		status, body = ownerAPI.doRaw(http.MethodGet, "/me/storage-servers", nil)
		Expect(status).To(Equal(http.StatusOK))
		decodeInto(body, &list)
		Expect(list.Servers).To(HaveLen(1))
		Expect(list.Servers[0].ID).To(Equal(server.ID))

		status, body = ownerAPI.doRaw(http.MethodGet, "/me/storage-servers/"+server.ID, nil)
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		status, _ = ownerAPI.doRaw(http.MethodGet, "/me/storage-servers/zzzzzzzzzz", nil)
		Expect(status).To(Equal(http.StatusNotFound))
		status, _ = ownerAPI.doRaw(http.MethodGet, "/me/storage-servers/not-an-id!", nil)
		Expect(status).To(Equal(http.StatusNotFound))

		stranger := newIdentity("byo-stranger")
		strangerAPI := newAPIClient(cli.apiURL, stranger.email, stranger.password)
		for _, call := range []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, "/me/storage-servers/" + server.ID, nil},
			{http.MethodPatch, "/me/storage-servers/" + server.ID, map[string]string{"name": "stolen"}},
			{http.MethodPost, "/me/storage-servers/" + server.ID + "/test", nil},
			{http.MethodDelete, "/me/storage-servers/" + server.ID, nil},
		} {
			status, body := strangerAPI.doRaw(call.method, call.path, call.body)
			Expect(status).To(Equal(http.StatusNotFound), "another user's server is invisible: %s %s: %s", call.method, call.path, body)
		}

		var tested struct {
			OK    bool   `json:"ok"`
			Check string `json:"check"`
		}
		status, body = ownerAPI.doRaw(http.MethodPost, "/me/storage-servers/"+server.ID+"/test", nil)
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		decodeInto(body, &tested)
		Expect(tested.OK).To(BeTrue(), "%s", body)

		status, body = ownerAPI.doRaw(http.MethodPatch, "/me/storage-servers/"+server.ID, map[string]string{"name": "renamed"})
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		decodeInto(body, &server)
		Expect(server.Name).To(Equal("renamed"))
		for _, patch := range []map[string]string{{"bucket": "other"}, {"prefix": "other"}} {
			status, body = ownerAPI.doRaw(http.MethodPatch, "/me/storage-servers/"+server.ID, patch)
			Expect(status).To(Equal(http.StatusBadRequest), "%s", body)
			Expect(string(body)).To(ContainSubstring("bucket and prefix cannot be changed"))
		}
		status, body = ownerAPI.doRaw(http.MethodPatch, "/me/storage-servers/"+server.ID, map[string]string{"secret_access_key": ""})
		Expect(status).To(Equal(http.StatusBadRequest), "%s", body)
		status, body = ownerAPI.doRaw(http.MethodPatch, "/me/storage-servers/"+server.ID, map[string]string{"secret_access_key": "not-the-secret"})
		Expect(status).To(Equal(http.StatusBadRequest), "a new secret is probed before it is kept: %s", body)
		status, body = ownerAPI.doRaw(http.MethodPost, "/me/storage-servers/"+server.ID+"/test", nil)
		Expect(status).To(Equal(http.StatusOK))
		decodeInto(body, &tested)
		Expect(tested.OK).To(BeTrue(), "the refused secret did not replace the working one: %s", body)

		status, body = ownerAPI.doRaw(http.MethodDelete, "/me/storage-servers/"+server.ID, nil)
		Expect(status).To(Equal(http.StatusNoContent), "%s", body)
		status, _ = ownerAPI.doRaw(http.MethodGet, "/me/storage-servers/"+server.ID, nil)
		Expect(status).To(Equal(http.StatusNotFound))
	})

	It("stores a project's content in the organization's own bucket", func() {
		if cli.edition == "free" {
			return
		}
		target := byoStorage()
		owner := newIdentity("byo-org-owner")
		ownerAPI := newAPIClient(cli.apiURL, owner.email, owner.password)
		org := "byo-org-" + randSuffix(4)
		ownerAPI.createOrg(org, org)
		prefix := "e2e-" + randSuffix(4)

		status, body := ownerAPI.doRaw(http.MethodPost, "/me/storage-servers", target.request("own bucket", prefix))
		Expect(status).To(Equal(http.StatusCreated), "%s", body)
		var server storageServerJSON
		decodeInto(body, &server)

		var def orgStorageServerJSON
		status, body = ownerAPI.doRaw(http.MethodGet, "/orgs/"+org+"/storage-server", nil)
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		decodeInto(body, &def)
		Expect(def.Server).To(BeNil())
		Expect(def.CanManage).To(BeTrue(), "the org owner is its billing owner")

		member := newIdentity("byo-member")
		ownerAPI.inviteOrgMember(org, member.email, "member")
		memberAPI := newAPIClient(cli.apiURL, member.email, member.password)
		memberAPI.acceptInvitation(org)
		status, body = memberAPI.doRaw(http.MethodGet, "/orgs/"+org+"/storage-server", nil)
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		decodeInto(body, &def)
		Expect(def.CanManage).To(BeFalse())
		status, body = memberAPI.doRaw(http.MethodPut, "/orgs/"+org+"/storage-server", map[string]string{"storage_server_id": server.ID})
		Expect(status).To(Equal(http.StatusForbidden), "%s", body)
		outsider := newAPIClient(cli.apiURL, newIdentity("byo-outsider").email, "identity-password-123")
		status, _ = outsider.doRaw(http.MethodGet, "/orgs/"+org+"/storage-server", nil)
		Expect(status).To(Equal(http.StatusForbidden), "non-members cannot read the default")

		status, body = ownerAPI.doRaw(http.MethodPut, "/orgs/"+org+"/storage-server", map[string]string{"storage_server_id": "bad id"})
		Expect(status).To(Equal(http.StatusBadRequest), "%s", body)
		status, body = ownerAPI.doRaw(http.MethodPut, "/orgs/"+org+"/storage-server", map[string]string{"storage_server_id": server.ID})
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		decodeInto(body, &def)
		Expect(def.Server).NotTo(BeNil())
		Expect(def.Server.ID).To(Equal(server.ID))
		Expect(def.Server.Name).To(Equal("own bucket"))

		// New projects land on the default; existing data never moves.
		ownerAPI.createProject(org, "art", "art")
		var where projectStorageJSON
		status, body = ownerAPI.doRaw(http.MethodGet, "/orgs/"+org+"/projects/art/storage", nil)
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		decodeInto(body, &where)
		Expect(where.External).To(BeTrue())
		Expect(where.Server.ID).To(Equal(server.ID))
		status, _ = memberAPI.doRaw(http.MethodGet, "/orgs/"+org+"/projects/art/storage", nil)
		Expect(status).To(Equal(http.StatusNotFound), "the storage location needs project read access")

		repoURL := repoURLFor(org, "art")
		parent := workspace()
		dir := cloneRepoAs(owner, parent, repoURL, "work")
		content := hex.EncodeToString(randomBytes(4096))
		writeText(dir, "art.txt", content)
		Expect(runNipaAs(owner, dir, "add", "art.txt").ExitCode).To(Equal(0))
		push := runNipaAs(owner, dir, "push", "-m", uniqueMessage("own storage content"))
		Expect(push.ExitCode).To(Equal(0), push.Output())

		Expect(target.objectsUnder(prefix)).NotTo(BeEmpty(), "the chunks were written to the org's bucket")
		other := cloneRepoAs(owner, parent, repoURL, "other")
		Expect(readText(other, "art.txt")).To(Equal(content), "clones read straight from the own bucket")

		status, storage := ownerAPI.getOrgStorage(org)
		Expect(status).To(Equal(http.StatusOK))
		used, _ := projectStorageBytes(storage, "art")
		Expect(used).To(BeZero(), "own storage is not billed")

		// An explicit server at creation must belong to the billing owner.
		status, body = ownerAPI.doRaw(http.MethodDelete, "/orgs/"+org+"/storage-server", nil)
		Expect(status).To(Equal(http.StatusNoContent), "%s", body)
		status, body = ownerAPI.doRaw(http.MethodGet, "/orgs/"+org+"/storage-server", nil)
		Expect(status).To(Equal(http.StatusOK))
		decodeInto(body, &def)
		Expect(def.Server).To(BeNil())
		ownerAPI.createProject(org, "plain", "plain")
		status, body = ownerAPI.doRaw(http.MethodGet, "/orgs/"+org+"/projects/plain/storage", nil)
		Expect(status).To(Equal(http.StatusOK))
		decodeInto(body, &where)
		Expect(where.External).To(BeFalse())
		Expect(where.Server).To(BeNil())
		status, body = ownerAPI.doRaw(http.MethodGet, "/orgs/"+org+"/projects/art/storage", nil)
		Expect(status).To(Equal(http.StatusOK))
		decodeInto(body, &where)
		Expect(where.External).To(BeTrue(), "clearing the default keeps existing projects where they are")

		status, body = ownerAPI.doRaw(http.MethodPost, "/orgs/"+org+"/projects", map[string]string{
			"name": "explicit", "slug": "explicit", "storage_server_id": server.ID,
		})
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		status, body = ownerAPI.doRaw(http.MethodGet, "/orgs/"+org+"/projects/explicit/storage", nil)
		Expect(status).To(Equal(http.StatusOK))
		decodeInto(body, &where)
		Expect(where.External).To(BeTrue())
		status, body = api.doRaw(http.MethodPost, "/orgs/"+org+"/projects", map[string]string{
			"name": "admin-pick", "slug": "admin-pick", "storage_server_id": server.ID,
		})
		Expect(status).To(Equal(http.StatusNotFound), "another user's server cannot be chosen: %s", body)

		status, body = ownerAPI.doRaw(http.MethodDelete, "/me/storage-servers/"+server.ID, nil)
		Expect(status).To(Equal(http.StatusConflict), "a server in use cannot be deleted: %s", body)
	})
})
