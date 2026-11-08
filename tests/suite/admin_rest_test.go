package suite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

// chunkSigningKey mirrors CHUNK_URL_SIGNING_KEY in tests/run.sh.
const chunkSigningKey = "client-e2e-chunk-signing-key-0123456789"

const refreshCookie = "nipa_refresh"

type authSession struct {
	access  string
	refresh string
}

func authRequest(path, refresh string, body any) (int, []byte, *http.Response) {
	GinkgoHelper()
	var reader io.Reader = bytes.NewReader(nil)
	if body != nil {
		data, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(http.MethodPost, cli.apiURL+"/auth"+path, reader)
	Expect(err).NotTo(HaveOccurred())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if refresh != "" {
		req.AddCookie(&http.Cookie{Name: refreshCookie, Value: refresh})
	}
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return resp.StatusCode, data, resp
}

func sessionFrom(status int, data []byte, resp *http.Response) authSession {
	GinkgoHelper()
	Expect(status).To(Equal(http.StatusOK), string(data))
	var out struct {
		AccessToken string `json:"access_token"`
	}
	Expect(json.Unmarshal(data, &out)).To(Succeed())
	Expect(out.AccessToken).NotTo(BeEmpty())
	s := authSession{access: out.AccessToken}
	for _, c := range resp.Cookies() {
		if c.Name == refreshCookie {
			s.refresh = c.Value
		}
	}
	Expect(s.refresh).NotTo(BeEmpty(), "no refresh cookie set")
	return s
}

func passwordLogin(email, password string) authSession {
	GinkgoHelper()
	return sessionFrom(authRequest("/login", "", map[string]string{"email": email, "password": password}))
}

func loginStatus(email, password string) int {
	GinkgoHelper()
	status, _, _ := authRequest("/login", "", map[string]string{"email": email, "password": password})
	return status
}

func refreshStatus(refresh string) int {
	GinkgoHelper()
	status, _, _ := authRequest("/refresh", refresh, nil)
	return status
}

type orgJSON struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
}

func myOrgSlugs(c *apiClient) []string {
	GinkgoHelper()
	var orgs []orgJSON
	c.do(http.MethodGet, "/orgs", nil, &orgs, true)
	slugs := make([]string, 0, len(orgs))
	for _, o := range orgs {
		slugs = append(slugs, o.Slug)
	}
	return slugs
}

type commitJSON struct {
	ID        string `json:"id"`
	Parent1ID string `json:"parent_1_id"`
	Message   string `json:"message"`
}

type treeJSON struct {
	Path    string `json:"path"`
	Entries []struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Type string `json:"type"`
	} `json:"entries"`
}

func treeNames(c *apiClient, base, query string) (int, []string) {
	GinkgoHelper()
	status, data := c.doRaw(http.MethodGet, base+"/tree?"+query, nil)
	if status != http.StatusOK {
		return status, nil
	}
	var tree treeJSON
	Expect(json.Unmarshal(data, &tree)).To(Succeed())
	names := []string{}
	for _, e := range tree.Entries {
		names = append(names, e.Type+":"+e.Name)
	}
	return status, names
}

func chunkRequest(method, path string, body []byte) (int, []byte) {
	GinkgoHelper()
	target := path
	if strings.HasPrefix(path, "/") {
		target = "http://" + cli.host + path
	}
	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	Expect(err).NotTo(HaveOccurred())
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return resp.StatusCode, data
}

func withQuery(path string, edit func(url.Values)) string {
	GinkgoHelper()
	u, err := url.Parse(path)
	Expect(err).NotTo(HaveOccurred())
	q := u.Query()
	edit(q)
	u.RawQuery = q.Encode()
	return u.String()
}

func serverUploadURL(project, hash string, size int64) *pb.PresignedChunkUrl {
	GinkgoHelper()
	conn, err := grpc.NewClient(cli.host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+api.token)
	resp, err := pb.NewNipaServiceClient(conn).GetChunkUploadUrls(ctx, &pb.GetChunkUploadUrlsRequest{
		Context: &pb.ProjectContext{Org: orgSlug, Project: project},
		Chunks:  []*pb.ChunkRef{{Hash: hash, SizeBytes: size}},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.GetUrls()).To(HaveLen(1))
	return resp.GetUrls()[0]
}

var _ = Describe("auth REST refresh and logout", func() {
	It("rotates the refresh cookie and revokes it on logout", func() {
		id := newIdentity("auth-refresh")
		first := passwordLogin(id.email, id.password)

		second := sessionFrom(authRequest("/refresh", first.refresh, nil))
		Expect(second.refresh).NotTo(Equal(first.refresh))
		Expect(newAPIClientWithToken(cli.apiURL, second.access).me()).To(Equal(id.userID))

		Expect(refreshStatus(first.refresh)).To(Equal(http.StatusNotFound), "a rotated refresh token is single-use")
		Expect(refreshStatus("")).To(Equal(http.StatusUnauthorized))
		Expect(refreshStatus("not-a-token")).To(Equal(http.StatusNotFound))

		status, data, resp := authRequest("/logout", second.refresh, nil)
		Expect(status).To(Equal(http.StatusOK), string(data))
		cleared := false
		for _, c := range resp.Cookies() {
			if c.Name == refreshCookie && c.Value == "" && c.MaxAge < 0 {
				cleared = true
			}
		}
		Expect(cleared).To(BeTrue(), "logout clears the refresh cookie")
		Expect(refreshStatus(second.refresh)).To(Equal(http.StatusNotFound))

		status, _, _ = authRequest("/logout", "", nil)
		Expect(status).To(Equal(http.StatusOK), "logout without a cookie is a no-op")
	})
})

var _ = Describe("users REST API", func() {
	It("updates the caller's profile and changes their password", func() {
		id := newIdentity("user-self")
		self := newAPIClient(cli.apiURL, id.email, id.password)
		session := passwordLogin(id.email, id.password)

		var profile struct {
			Name        string `json:"name"`
			NotifyEmail bool   `json:"notify_email"`
		}
		self.do(http.MethodPatch, "/me", map[string]any{"name": "Renamed Self", "notify_email": false}, &profile, true)
		Expect(profile.Name).To(Equal("Renamed Self"))
		Expect(profile.NotifyEmail).To(BeFalse())
		status, _ := self.doRaw(http.MethodPatch, "/me", map[string]string{"name": "  "})
		Expect(status).To(Equal(http.StatusBadRequest))

		status, data := self.doRaw(http.MethodPost, "/me/password", map[string]string{
			"old_password": "wrong-password-123", "new_password": "brand-new-password-1",
		})
		Expect(status).To(Equal(http.StatusBadRequest))
		Expect(string(data)).To(ContainSubstring("current password is incorrect"))
		status, _ = self.doRaw(http.MethodPost, "/me/password", map[string]string{
			"old_password": id.password, "new_password": "short",
		})
		Expect(status).To(Equal(http.StatusBadRequest))

		self.do(http.MethodPost, "/me/password", map[string]string{
			"old_password": id.password, "new_password": "brand-new-password-1",
		}, nil, true)
		Expect(loginStatus(id.email, id.password)).To(Equal(http.StatusBadRequest))
		Expect(loginStatus(id.email, "brand-new-password-1")).To(Equal(http.StatusOK))
		Expect(refreshStatus(session.refresh)).NotTo(Equal(http.StatusOK), "a password change revokes refresh tokens")
	})

	It("lets an admin update, reset and deactivate users while non-admins are forbidden", func() {
		target := newIdentity("user-target")
		outsider := newIdentity("user-outsider")
		outsiderAPI := newAPIClient(cli.apiURL, outsider.email, outsider.password)
		base := "/users/" + target.userID

		for _, call := range []struct {
			method string
			path   string
			body   any
		}{
			{http.MethodGet, "/users", nil},
			{http.MethodPatch, base, map[string]string{"email": "hijack-" + randSuffix(3) + "@example.com"}},
			{http.MethodPost, base + "/password", map[string]string{"password": "hijacked-password-1"}},
			{http.MethodDelete, base, nil},
			{http.MethodPatch, base + "/admin", map[string]bool{"is_admin": true}},
		} {
			status, data := outsiderAPI.doRaw(call.method, call.path, call.body)
			Expect(status).To(Equal(http.StatusForbidden), "%s %s: %s", call.method, call.path, data)
		}

		otherAdmin := newIdentity("user-admin-b").promote()
		plainAdmin := newIdentity("user-admin-a").promote()
		status, _ := newAPIClient(cli.apiURL, plainAdmin.email, plainAdmin.password).
			doRaw(http.MethodPost, "/users/"+otherAdmin.userID+"/password", map[string]string{"password": "admin-reset-password-1"})
		Expect(status).To(Equal(http.StatusForbidden), "a plain admin cannot manage another admin")

		newEmail := "renamed-" + randSuffix(4) + "@example.com"
		var updated struct {
			Email string `json:"email"`
		}
		api.do(http.MethodPatch, base, map[string]string{"email": newEmail}, &updated, true)
		Expect(updated.Email).To(Equal(newEmail))
		status, _ = api.doRaw(http.MethodPatch, base, map[string]string{"email": "not-an-email"})
		Expect(status).To(Equal(http.StatusBadRequest))
		status, _ = api.doRaw(http.MethodPatch, base, map[string]string{"email": outsider.email})
		Expect(status).To(Equal(http.StatusConflict))
		Expect(loginStatus(target.email, target.password)).To(Equal(http.StatusBadRequest))
		Expect(loginStatus(newEmail, target.password)).To(Equal(http.StatusOK))

		status, _ = api.doRaw(http.MethodPost, base+"/password", map[string]string{"password": "short"})
		Expect(status).To(Equal(http.StatusBadRequest))
		api.do(http.MethodPost, base+"/password", map[string]string{"password": "admin-reset-password-1"}, nil, true)
		Expect(loginStatus(newEmail, target.password)).To(Equal(http.StatusBadRequest))
		session := passwordLogin(newEmail, "admin-reset-password-1")

		status, _ = api.doRaw(http.MethodDelete, "/users/"+api.me(), nil)
		Expect(status).To(Equal(http.StatusBadRequest), "nobody can deactivate themselves")

		api.do(http.MethodDelete, base, nil, nil, true)
		Expect(loginStatus(newEmail, "admin-reset-password-1")).To(Equal(http.StatusBadRequest))
		Expect(refreshStatus(session.refresh)).NotTo(Equal(http.StatusOK))
		var users []struct {
			ID      string `json:"id"`
			Deleted bool   `json:"deleted"`
		}
		api.do(http.MethodGet, "/users", nil, &users, true)
		for _, u := range users {
			if u.ID == target.userID {
				Expect(u.Deleted).To(BeTrue())
			}
		}
	})
})

var _ = Describe("organizations REST API", func() {
	It("lists the caller's orgs and rejects duplicate, invalid and reserved slugs", func() {
		id := newIdentity("org-list")
		c := newAPIClient(cli.apiURL, id.email, id.password)
		Expect(myOrgSlugs(c)).To(BeEmpty())

		org := "org-list-" + randSuffix(4)
		c.createOrg("List Org", org)
		var orgs []orgJSON
		c.do(http.MethodGet, "/orgs", nil, &orgs, true)
		Expect(orgs).To(ConsistOf(And(HaveField("Slug", org), HaveField("Name", "List Org"), HaveField("Role", "owner"))))
		Expect(myOrgSlugs(api)).NotTo(ContainElement(org), "GET /orgs is the caller's memberships, not every org")

		status, _ := c.doRaw(http.MethodPost, "/orgs", map[string]string{"name": "Dup", "slug": org})
		Expect(status).To(Equal(http.StatusConflict))
		for _, slug := range []string{"Bad Slug!", "admin", "settings"} {
			status, data := c.doRaw(http.MethodPost, "/orgs", map[string]string{"name": "x", "slug": slug})
			Expect(status).To(Equal(http.StatusBadRequest), "%s: %s", slug, data)
		}
		status, _ = c.doRaw(http.MethodPost, "/orgs", map[string]string{"name": " ", "slug": "blank-" + randSuffix(3)})
		Expect(status).To(Equal(http.StatusBadRequest))
	})

	It("promotes a member to owner and revokes access on removal", func() {
		owner := newIdentity("org-role-owner")
		member := newIdentity("org-role-member")
		ownerAPI := newAPIClient(cli.apiURL, owner.email, owner.password)
		memberAPI := newAPIClient(cli.apiURL, member.email, member.password)

		org := "org-role-" + randSuffix(4)
		ownerAPI.createOrg(org, org)
		base := "/orgs/" + org + "/members/"
		api.do(http.MethodPost, "/orgs/"+org+"/members", map[string]string{"user_id": member.userID, "role": "member"}, nil, true)
		Expect(myOrgSlugs(memberAPI)).To(ContainElement(org))

		status, _ := memberAPI.doRaw(http.MethodPost, "/orgs/"+org+"/projects", map[string]string{"name": "m", "slug": "m"})
		Expect(status).To(Equal(http.StatusForbidden))
		status, _ = memberAPI.doRaw(http.MethodPatch, base+member.userID, map[string]string{"role": "owner"})
		Expect(status).To(Equal(http.StatusForbidden), "members cannot promote themselves")
		status, _ = ownerAPI.doRaw(http.MethodPatch, base+member.userID, map[string]string{"role": "admin"})
		Expect(status).To(Equal(http.StatusBadRequest))

		var promoted struct {
			UserID string `json:"user_id"`
			Role   string `json:"role"`
		}
		ownerAPI.do(http.MethodPatch, base+member.userID, map[string]string{"role": "owner"}, &promoted, true)
		Expect(promoted.UserID).To(Equal(member.userID))
		Expect(promoted.Role).To(Equal("owner"))

		memberAPI.createProject(org, "promoted", "promoted")
		url := repoURLFor(org, "promoted")
		cloneRepoAs(member, workspace(), url, "work")

		ownerAPI.do(http.MethodDelete, base+member.userID, nil, nil, true)
		Expect(myOrgSlugs(memberAPI)).NotTo(ContainElement(org))
		status, _ = memberAPI.doRaw(http.MethodPost, "/orgs/"+org+"/projects", map[string]string{"name": "again", "slug": "again"})
		Expect(status).To(Equal(http.StatusForbidden))
		denied := runNipaAs(member, workspace(), "clone", url, "denied")
		Expect(denied.ExitCode).NotTo(Equal(0), denied.Output())

		status, _ = ownerAPI.doRaw(http.MethodDelete, base+owner.userID, nil)
		Expect(status).To(Equal(http.StatusConflict), "the last owner cannot leave")
	})
})

var _ = Describe("projects REST API", func() {
	It("gets, updates and deletes a project", func() {
		project := newProject("rest-project")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		base := "/orgs/" + orgSlug + "/projects/" + project
		reader := newIdentity("project-reader")
		readerAPI := newAPIClient(cli.apiURL, reader.email, reader.password)

		var got struct {
			ID          string `json:"id"`
			Slug        string `json:"slug"`
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		api.do(http.MethodGet, base, nil, &got, true)
		Expect(got.Slug).To(Equal(project))
		Expect(got.ID).NotTo(BeEmpty())

		status, _ := readerAPI.doRaw(http.MethodGet, base, nil)
		Expect(status).To(Equal(http.StatusNotFound), "an unreadable project looks absent")
		api.createPathRule(orgSlug, project, reader.userID, "/", 1)
		status, _ = readerAPI.doRaw(http.MethodGet, base, nil)
		Expect(status).To(Equal(http.StatusOK))
		status, _ = readerAPI.doRaw(http.MethodPatch, base, map[string]string{"name": "taken over"})
		Expect(status).To(Equal(http.StatusForbidden))
		status, _ = readerAPI.doRaw(http.MethodDelete, base, nil)
		Expect(status).To(Equal(http.StatusForbidden))

		api.do(http.MethodPatch, base, map[string]string{"name": "Renamed", "description": "new description"}, &got, true)
		Expect(got.Name).To(Equal("Renamed"))
		Expect(got.Description).To(Equal("new description"))
		Expect(got.Slug).To(Equal(project))
		api.do(http.MethodGet, base, nil, &got, true)
		Expect(got.Name).To(Equal("Renamed"))
		Expect(got.Description).To(Equal("new description"))
		status, _ = api.doRaw(http.MethodPatch, base, map[string]string{"name": ""})
		Expect(status).To(Equal(http.StatusBadRequest))

		api.do(http.MethodDelete, base, nil, nil, true)
		status, _ = api.doRaw(http.MethodGet, base, nil)
		Expect(status).To(Equal(http.StatusNotFound))
		status, _ = api.doRaw(http.MethodDelete, base, nil)
		Expect(status).To(Equal(http.StatusNotFound))
		denied := runNipa(workspace(), "clone", repoURLFor(orgSlug, project), "gone")
		Expect(denied.ExitCode).NotTo(Equal(0), denied.Output())
	})
})

type branchListEntry struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

func seedRenamedDefault(project string) {
	GinkgoHelper()
	seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
	base := "/orgs/" + orgSlug + "/projects/" + project + "/branches"
	api.do(http.MethodPost, base, map[string]string{"name": "feature", "from": "main"}, nil, true)
	seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v2\n")}, "main moves on")
	api.do(http.MethodPatch, base+"/feature", map[string]string{"name": "release"}, nil, true)
	api.do(http.MethodPost, base+"/release/default", nil, nil, true)
}

var _ = Describe("branches REST API", func() {
	It("renames a branch and sets it as the default", func() {
		project := newProject("rest-branch")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v1\n")}, "seed")
		base := "/orgs/" + orgSlug + "/projects/" + project + "/branches"
		api.do(http.MethodPost, base, map[string]string{"name": "feature", "from": "main"}, nil, true)
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("v2\n")}, "main moves on")

		var renamed branchListEntry
		api.do(http.MethodPatch, base+"/feature", map[string]string{"name": "release"}, &renamed, true)
		Expect(renamed.Name).To(Equal("release"))
		Expect(renamed.IsDefault).To(BeFalse())
		status, _ := api.doRaw(http.MethodPatch, base+"/release", map[string]string{"name": "main"})
		Expect(status).To(Equal(http.StatusConflict))
		status, _ = api.doRaw(http.MethodPatch, base+"/feature", map[string]string{"name": "other"})
		Expect(status).To(Equal(http.StatusNotFound))

		writer := newIdentity("branch-writer")
		api.createPathRule(orgSlug, project, writer.userID, "/", 3)
		status, _ = newAPIClient(cli.apiURL, writer.email, writer.password).doRaw(http.MethodPost, base+"/release/default", nil)
		Expect(status).To(Equal(http.StatusForbidden), "set-default needs project admin")
		status, _ = api.doRaw(http.MethodPost, base+"/missing/default", nil)
		Expect(status).To(Equal(http.StatusNotFound))

		var def branchListEntry
		api.do(http.MethodPost, base+"/release/default", nil, &def, true)
		Expect(def.Name).To(Equal("release"))
		Expect(def.IsDefault).To(BeTrue())

		var branches []branchListEntry
		api.do(http.MethodGet, base, nil, &branches, true)
		Expect(branches).To(ConsistOf(
			branchListEntry{Name: "release", IsDefault: true},
			branchListEntry{Name: "main", IsDefault: false},
		))

		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "release", "--branch", "release")
		Expect(readText(dir, "a.txt")).To(Equal("v1\n"))
		list := runNipa(dir, "branch", "-a")
		Expect(list.ExitCode).To(Equal(0), list.Output())
		Expect(list.Output()).To(ContainSubstring("release *"))
		Expect(list.Output()).To(ContainSubstring("main"))
		Expect(list.Output()).NotTo(ContainSubstring("feature"))
	})

	It("clones the new default branch when --branch is omitted", func() {
		project := newProject("rest-branch-default")
		seedRenamedDefault(project)

		dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "fresh")
		Expect(loadRepoConfig(dir).Branch).To(Equal("release"))
		Expect(readText(dir, "a.txt")).To(Equal("v1\n"))
	})
})

var _ = Describe("browser REST API", func() {
	It("serves commits, diffs, trees and blobs and hides read-filtered paths", func() {
		project := newProject("rest-browse")
		first := seedRepo(orgSlug, project, map[string][]byte{
			"README.md":       []byte("hello\n"),
			"src/main.go":     []byte("package main\n"),
			"docs/secret.txt": []byte("secret\n"),
		}, "browse seed")
		second := seedRepo(orgSlug, project, map[string][]byte{"README.md": []byte("hello\nworld\n")}, "browse edit")
		base := "/orgs/" + orgSlug + "/projects/" + project

		var commits []commitJSON
		api.do(http.MethodGet, base+"/commits", nil, &commits, true)
		Expect(commits).To(HaveLen(2))
		Expect(commits[0].Message).To(Equal(second))
		Expect(commits[1].Message).To(Equal(first))
		Expect(commits[0].Parent1ID).To(Equal(commits[1].ID))

		var limited []commitJSON
		api.do(http.MethodGet, base+"/commits?limit=1", nil, &limited, true)
		Expect(limited).To(HaveLen(1))
		var byPath []commitJSON
		api.do(http.MethodGet, base+"/commits?path=src", nil, &byPath, true)
		Expect(byPath).To(HaveLen(1))
		Expect(byPath[0].ID).To(Equal(commits[1].ID))

		var diffOut struct {
			CommitID string `json:"commit_id"`
			BaseID   string `json:"base_id"`
			Files    []struct {
				Path      string `json:"path"`
				Status    string `json:"status"`
				Additions int    `json:"additions"`
				Deletions int    `json:"deletions"`
				Hunks     []struct {
					Lines []struct {
						Kind string `json:"kind"`
						Text string `json:"text"`
					} `json:"lines"`
				} `json:"hunks"`
			} `json:"files"`
		}
		api.do(http.MethodGet, base+"/commits/"+commits[0].ID+"/diff", nil, &diffOut, true)
		Expect(diffOut.CommitID).To(Equal(commits[0].ID))
		Expect(diffOut.BaseID).To(Equal(commits[1].ID))
		Expect(diffOut.Files).To(HaveLen(1))
		Expect(diffOut.Files[0].Path).To(Equal("README.md"))
		Expect(diffOut.Files[0].Status).To(Equal("M"))
		Expect(diffOut.Files[0].Additions).To(Equal(1))
		Expect(diffOut.Files[0].Deletions).To(Equal(0))
		Expect(diffOut.Files[0].Hunks).NotTo(BeEmpty())
		Expect(diffOut.Files[0].Hunks[0].Lines).To(ContainElement(And(HaveField("Kind", "add"), HaveField("Text", "world"))))

		api.do(http.MethodGet, base+"/commits/"+commits[1].ID+"/diff", nil, &diffOut, true)
		Expect(diffOut.Files).To(HaveLen(3))
		for _, f := range diffOut.Files {
			Expect(f.Status).To(Equal("A"))
		}
		status, _ := api.doRaw(http.MethodGet, base+"/commits/zzzzzzzzzz/diff", nil)
		Expect(status).To(Equal(http.StatusNotFound))

		status, names := treeNames(api, base, "")
		Expect(status).To(Equal(http.StatusOK))
		Expect(names).To(Equal([]string{"tree:docs", "tree:src", "file:README.md"}))
		_, names = treeNames(api, base, "path=src")
		Expect(names).To(Equal([]string{"file:main.go"}))
		status, _ = treeNames(api, base, "path=nope")
		Expect(status).To(Equal(http.StatusNotFound))

		status, blob := api.doRaw(http.MethodGet, base+"/blob?path=README.md", nil)
		Expect(status).To(Equal(http.StatusOK))
		Expect(string(blob)).To(Equal("hello\nworld\n"))
		status, blob = api.doRaw(http.MethodGet, base+"/blob?path=README.md&rev="+commits[1].ID, nil)
		Expect(status).To(Equal(http.StatusOK))
		Expect(string(blob)).To(Equal("hello\n"))
		status, blob = api.doRaw(http.MethodGet, base+"/blob?path=docs/secret.txt&rev=main", nil)
		Expect(status).To(Equal(http.StatusOK))
		Expect(string(blob)).To(Equal("secret\n"))
		status, _ = api.doRaw(http.MethodGet, base+"/blob?path=missing.txt", nil)
		Expect(status).To(Equal(http.StatusNotFound))

		viewer := newIdentity("browse-viewer")
		viewerAPI := newAPIClient(cli.apiURL, viewer.email, viewer.password)
		api.createPathRule(orgSlug, project, viewer.userID, "src", 1)
		status, names = treeNames(viewerAPI, base, "")
		Expect(status).To(Equal(http.StatusOK))
		Expect(names).To(Equal([]string{"tree:src"}))
		status, blob = viewerAPI.doRaw(http.MethodGet, base+"/blob?path=src/main.go", nil)
		Expect(status).To(Equal(http.StatusOK))
		Expect(string(blob)).To(Equal("package main\n"))
		status, _ = viewerAPI.doRaw(http.MethodGet, base+"/blob?path=docs/secret.txt", nil)
		Expect(status).To(Equal(http.StatusNotFound))
		status, _ = treeNames(viewerAPI, base, "path=docs")
		Expect(status).To(Equal(http.StatusNotFound))
		var filtered struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		viewerAPI.do(http.MethodGet, base+"/commits/"+commits[1].ID+"/diff", nil, &filtered, true)
		Expect(filtered.Files).To(ConsistOf(HaveField("Path", "src/main.go")))
	})
})

var _ = Describe("webhook REST API by id", func() {
	It("gets a webhook by id and stops deliveries once it is deleted", func() {
		project := newProject("rest-hook")
		seedRepo(orgSlug, project, map[string][]byte{"a.txt": []byte("a\n")}, "seed")
		fixture := newWebhook(project, []string{"push"}, map[string]any{"path_prefix": "src"})
		path := "/orgs/" + orgSlug + "/projects/" + project + "/webhooks/" + fixture.hook.ID

		var got webhookJSON
		api.do(http.MethodGet, path, nil, &got, true)
		Expect(got.ID).To(Equal(fixture.hook.ID))
		Expect(got.Name).To(Equal("e2e-hook"))
		Expect(got.URL).To(Equal(fixture.receiver.url("/hook")))
		Expect(got.Events).To(Equal([]string{"push"}))
		Expect(got.PathPrefix).To(Equal("src"))
		Expect(got.IsActive).To(BeTrue())
		Expect(got.Secret).To(BeEmpty(), "the secret is only returned on create and rotate")

		outsider := newIdentity("hook-outsider")
		status, _ := newAPIClient(cli.apiURL, outsider.email, outsider.password).doRaw(http.MethodGet, path, nil)
		Expect(status).NotTo(Equal(http.StatusOK))

		api.do(http.MethodDelete, path, nil, nil, true)
		status, _ = api.doRaw(http.MethodGet, path, nil)
		Expect(status).To(Equal(http.StatusNotFound))
		status, _ = api.doRaw(http.MethodDelete, path, nil)
		Expect(status).To(Equal(http.StatusNotFound))

		seedRepo(orgSlug, project, map[string][]byte{"src/b.txt": []byte("b\n")}, "after delete")
		Consistently(func() int { return len(fixture.receiver.all()) }, "2s", "100ms").Should(BeZero())
	})
})

var _ = Describe("signed chunk transfer routes", func() {
	It("rejects bad signatures, expired URLs and mismatched uploads", func() {
		project := newProject("rest-chunk")
		data := randomBytes(1024)
		hash := chunker.Sum(data).String()
		size := int64(len(data))
		future := time.Now().Add(time.Hour).Unix()

		put := serverUploadURL(project, hash, size)
		Expect(put.GetAlreadyStored()).To(BeFalse())
		upload := put.GetUrl()
		if !strings.HasPrefix(upload, "/api/chunks/") {
			upload = chunkurl.UploadPath(chunkSigningKey, orgSlug, project, hash, size, future)
		}
		Expect(upload).To(HavePrefix("/api/chunks/" + orgSlug + "/" + project + "/" + hash))

		tampered := withQuery(upload, func(q url.Values) { q.Set("sig", strings.Repeat("0", 64)) })
		status, _ := chunkRequest(http.MethodPut, tampered, data)
		Expect(status).To(Equal(http.StatusForbidden))
		unsigned := withQuery(upload, func(q url.Values) { q.Del("sig") })
		status, _ = chunkRequest(http.MethodPut, unsigned, data)
		Expect(status).To(Equal(http.StatusForbidden))
		resized := withQuery(upload, func(q url.Values) { q.Set("size", strconv.FormatInt(size+1, 10)) })
		status, _ = chunkRequest(http.MethodPut, resized, append(data, 0))
		Expect(status).To(Equal(http.StatusForbidden), "the size is part of the signature")
		otherProject := strings.Replace(upload, "/"+project+"/", "/"+project+"-x/", 1)
		status, _ = chunkRequest(http.MethodPut, otherProject, data)
		Expect(status).To(Equal(http.StatusForbidden), "the url is bound to its project")
		status, _ = chunkRequest(http.MethodGet, upload, nil)
		Expect(status).To(Equal(http.StatusForbidden), "an upload url cannot download")

		expired := chunkurl.UploadPath(chunkSigningKey, orgSlug, project, hash, size, time.Now().Add(-time.Minute).Unix())
		status, _ = chunkRequest(http.MethodPut, expired, data)
		Expect(status).To(Equal(http.StatusForbidden))

		status, body := chunkRequest(http.MethodPut, upload, data[:512])
		Expect(status).To(Equal(http.StatusBadRequest), string(body))
		Expect(string(body)).To(ContainSubstring("size mismatch"))
		status, body = chunkRequest(http.MethodPut, upload, randomBytes(int(size)))
		Expect(status).To(Equal(http.StatusBadRequest), string(body))
		Expect(string(body)).To(ContainSubstring("hash mismatch"))

		download := chunkurl.DownloadPath(chunkSigningKey, orgSlug, project, hash, future)
		status, _ = chunkRequest(http.MethodGet, download, nil)
		Expect(status).To(Equal(http.StatusNotFound), "nothing was stored by the rejected uploads")

		status, body = chunkRequest(http.MethodPut, upload, data)
		Expect(status).To(Equal(http.StatusNoContent), string(body))
		status, body = chunkRequest(http.MethodGet, download, nil)
		Expect(status).To(Equal(http.StatusOK))
		Expect(body).To(Equal(data))

		staleDownload := chunkurl.DownloadPath(chunkSigningKey, orgSlug, project, hash, time.Now().Add(-time.Minute).Unix())
		status, _ = chunkRequest(http.MethodGet, staleDownload, nil)
		Expect(status).To(Equal(http.StatusForbidden))
		status, _ = chunkRequest(http.MethodGet, withQuery(download, func(q url.Values) { q.Set("sig", "bad") }), nil)
		Expect(status).To(Equal(http.StatusForbidden))
		status, _ = chunkRequest(http.MethodPut, download, data)
		Expect(status).To(Equal(http.StatusForbidden), "a download url cannot upload")
	})
})
