package suite_test

import (
	"encoding/json"
	"net/http"
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var invitationLink = regexp.MustCompile(`/invitations/([A-Za-z0-9_-]+)`)

type invitationJSON struct {
	ID      string `json:"id"`
	OrgSlug string `json:"org_slug"`
	Email   string `json:"email"`
	Role    string `json:"role"`
	Status  string `json:"status"`
}

func (c *apiClient) invitations(path string) []invitationJSON {
	GinkgoHelper()
	status, data := c.doRaw(http.MethodGet, path, nil)
	Expect(status).To(Equal(http.StatusOK), string(data))
	var out []invitationJSON
	Expect(json.Unmarshal(data, &out)).To(Succeed())
	return out
}

var _ = Describe("organization invitations", func() {
	It("invites by email, hides account existence and joins only on the invitee's consent", func() {
		owner := newIdentity("inv-owner")
		invitee := newIdentity("inv-invitee")
		bystander := newIdentity("inv-bystander")
		ownerAPI := newAPIClient(cli.apiURL, owner.email, owner.password)
		inviteeAPI := newAPIClient(cli.apiURL, invitee.email, invitee.password)
		bystanderAPI := newAPIClient(cli.apiURL, bystander.email, bystander.password)

		org := "inv-org-" + randSuffix(4)
		ownerAPI.createOrg(org, org)
		base := "/orgs/" + org

		By("answering identically for registered and unknown addresses")
		unknown := "nobody-" + randSuffix(5) + "@example.com"
		knownStatus, knownBody := ownerAPI.doRaw(http.MethodPost, base+"/invitations", map[string]string{"email": invitee.email})
		unknownStatus, unknownBody := ownerAPI.doRaw(http.MethodPost, base+"/invitations", map[string]string{"email": unknown})
		Expect(knownStatus).To(Equal(http.StatusAccepted), string(knownBody))
		Expect(unknownStatus).To(Equal(http.StatusAccepted), string(unknownBody))
		Expect(string(unknownBody)).To(MatchJSON(string(knownBody)))

		By("refusing the old add-by-id and non-owner callers")
		status, _ := ownerAPI.doRaw(http.MethodPost, base+"/members", map[string]string{"user_id": invitee.userID})
		Expect(status).To(Equal(http.StatusBadRequest))
		status, _ = bystanderAPI.doRaw(http.MethodPost, base+"/invitations", map[string]string{"email": unknown})
		Expect(status).To(Equal(http.StatusForbidden))
		status, _ = bystanderAPI.doRaw(http.MethodGet, base+"/invitations", nil)
		Expect(status).To(Equal(http.StatusForbidden))

		By("not adding the invitee before they accept")
		status, data := ownerAPI.doRaw(http.MethodGet, base+"/members", nil)
		Expect(status).To(Equal(http.StatusOK))
		Expect(string(data)).NotTo(ContainSubstring(invitee.email))

		By("mailing a one-time link to the invitee")
		mail := email.waitForSubject(invitee.email, "invited you to join")
		match := invitationLink.FindStringSubmatch(mail.Text)
		Expect(match).To(HaveLen(2), mail.Text)
		token := match[1]

		By("keeping the invitation invisible to everyone else")
		status, _ = bystanderAPI.doRaw(http.MethodGet, "/invitations/"+token, nil)
		Expect(status).To(Equal(http.StatusNotFound))
		status, _ = bystanderAPI.doRaw(http.MethodPost, "/invitations/"+token+"/accept", nil)
		Expect(status).To(Equal(http.StatusNotFound))
		status, _ = inviteeAPI.doRaw(http.MethodGet, "/invitations/not-the-token", nil)
		Expect(status).To(Equal(http.StatusNotFound))
		Expect(bystanderAPI.invitations("/me/invitations")).To(BeEmpty())

		mine := inviteeAPI.invitations("/me/invitations")
		Expect(mine).To(HaveLen(1))
		Expect(mine[0].OrgSlug).To(Equal(org))

		By("rotating the token on resend so the old link dies")
		status, _ = ownerAPI.doRaw(http.MethodPost, base+"/invitations/"+mine[0].ID+"/resend", nil)
		Expect(status).To(Equal(http.StatusAccepted))
		var fresh string
		Eventually(func() string {
			var last string
			for _, m := range email.all() {
				if contains(m.To, invitee.email) {
					if found := invitationLink.FindStringSubmatch(m.Text); len(found) == 2 {
						last = found[1]
					}
				}
			}
			fresh = last
			return last
		}, "10s", "50ms").ShouldNot(Equal(token))
		status, _ = inviteeAPI.doRaw(http.MethodPost, "/invitations/"+token+"/accept", nil)
		Expect(status).To(Equal(http.StatusNotFound))

		By("joining when the invitee accepts")
		status, data = inviteeAPI.doRaw(http.MethodGet, "/invitations/"+fresh, nil)
		Expect(status).To(Equal(http.StatusOK), string(data))
		status, data = inviteeAPI.doRaw(http.MethodPost, "/invitations/"+fresh+"/accept", nil)
		Expect(status).To(Equal(http.StatusOK), string(data))
		status, data = ownerAPI.doRaw(http.MethodGet, base+"/members", nil)
		Expect(status).To(Equal(http.StatusOK))
		Expect(string(data)).To(ContainSubstring(invitee.email))
		status, _ = inviteeAPI.doRaw(http.MethodPost, "/invitations/"+fresh+"/accept", nil)
		Expect(status).To(Equal(http.StatusNotFound), "an accepted link cannot be replayed")

		By("refusing to invite a current member and revoking pending invitations")
		status, _ = ownerAPI.doRaw(http.MethodPost, base+"/invitations", map[string]string{"email": invitee.email})
		Expect(status).To(Equal(http.StatusConflict))
		pending := ownerAPI.invitations(base + "/invitations?status=pending")
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].Email).To(Equal(unknown))
		status, _ = ownerAPI.doRaw(http.MethodDelete, base+"/invitations/"+pending[0].ID, nil)
		Expect(status).To(Equal(http.StatusOK))
		Expect(ownerAPI.invitations(base + "/invitations?status=pending")).To(BeEmpty())
		Expect(ownerAPI.invitations(base + "/invitations?status=revoked")).To(HaveLen(1))
	})

	It("lets an invitee decline and never adds a user through a role update", func() {
		owner := newIdentity("inv-owner2")
		invitee := newIdentity("inv-invitee2")
		ownerAPI := newAPIClient(cli.apiURL, owner.email, owner.password)
		inviteeAPI := newAPIClient(cli.apiURL, invitee.email, invitee.password)

		org := "inv-org-" + randSuffix(4)
		ownerAPI.createOrg(org, org)
		base := "/orgs/" + org

		ownerAPI.inviteOrgMember(org, invitee.email, "owner")
		mine := inviteeAPI.invitations("/me/invitations")
		Expect(mine).To(HaveLen(1))
		Expect(mine[0].Role).To(Equal("owner"))
		status, _ := inviteeAPI.doRaw(http.MethodPost, "/me/invitations/"+mine[0].ID+"/decline", nil)
		Expect(status).To(Equal(http.StatusOK))
		Expect(inviteeAPI.invitations("/me/invitations")).To(BeEmpty())
		status, _ = inviteeAPI.doRaw(http.MethodPost, "/me/invitations/"+mine[0].ID+"/accept", nil)
		Expect(status).To(Equal(http.StatusNotFound))

		status, _ = ownerAPI.doRaw(http.MethodPatch, base+"/members/"+invitee.userID, map[string]string{"role": "owner"})
		Expect(status).To(Equal(http.StatusNotFound))
	})
})
