package suite_test

import (
	"net/http"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type signupInviteJSON struct {
	ID        string     `json:"id"`
	Email     string     `json:"email"`
	InvitedBy string     `json:"invited_by"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type signupInviteListJSON struct {
	Invites []signupInviteJSON `json:"invites"`
}

type waitlistEntryJSON struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Name       string     `json:"name"`
	Source     string     `json:"source"`
	CreatedAt  time.Time  `json:"created_at"`
	NotifiedAt *time.Time `json:"notified_at"`
}

type waitlistListJSON struct {
	Entries    []waitlistEntryJSON `json:"entries"`
	NextCursor string              `json:"next_cursor"`
}

type termsStatusJSON struct {
	Version         string `json:"version"`
	AcceptedVersion string `json:"accepted_version"`
	AcceptedAt      string `json:"accepted_at"`
	Required        bool   `json:"required"`
}

type inviteOnlyCapabilitiesJSON struct {
	Registration bool     `json:"registration"`
	InviteOnly   bool     `json:"invite_only"`
	Providers    []string `json:"providers"`
	Terms        *struct {
		Version string `json:"version"`
	} `json:"terms"`
}

func (c *apiClient) signupInvites() signupInviteListJSON {
	GinkgoHelper()
	var out signupInviteListJSON
	c.do(http.MethodGet, "/admin/signup-invites", nil, &out, true)
	return out
}

func (c *apiClient) waitlist(query string) waitlistListJSON {
	GinkgoHelper()
	var out waitlistListJSON
	c.do(http.MethodGet, "/admin/waitlist"+query, nil, &out, true)
	return out
}

func findInvite(list signupInviteListJSON, address string) *signupInviteJSON {
	for i := range list.Invites {
		if list.Invites[i].Email == address {
			return &list.Invites[i]
		}
	}
	return nil
}

func findWaitlistEntry(list waitlistListJSON, address string) *waitlistEntryJSON {
	for i := range list.Entries {
		if list.Entries[i].Email == address {
			return &list.Entries[i]
		}
	}
	return nil
}

// inviteOnlyServer returns an admin client for the invite-only nipad-ee
// run.sh starts next to the main server (own database, same email receiver).
func inviteOnlyServer() (*apiClient, string) {
	GinkgoHelper()
	host := os.Getenv("NIPA_TEST_INVITEONLY_HOST")
	if host == "" {
		Skip("needs the invite-only server from tests/run.sh ee (NIPA_TEST_SAAS)")
	}
	return newAPIClient("http://"+host+"/api/v1", cli.user, cli.password), os.Getenv("NIPA_TEST_INVITEONLY_TERMS_VERSION")
}

// registerAndVerify confirms a pending sign-up through the emailed token and
// returns a client for the new account.
func registerAndVerify(c *apiClient, address, password string) *apiClient {
	GinkgoHelper()
	token := verificationToken(email.waitForSubject(address, "Confirm your email address"))
	status, body := c.doAnonRaw(http.MethodPost, "/auth/verify", map[string]string{"token": token, "password": password})
	Expect(status).To(Equal(http.StatusOK), "verify: %s", body)
	return newAPIClient(c.baseURL, address, password)
}

var _ = Describe("saas administration", func() {
	It("manages signup invites as a super admin", func() {
		saasEnabled()
		if cli.edition == "free" {
			for _, route := range []struct{ method, path string }{
				{http.MethodGet, "/admin/signup-invites"},
				{http.MethodPost, "/admin/signup-invites"},
				{http.MethodDelete, "/admin/signup-invites/1"},
			} {
				status, body := api.doRaw(route.method, route.path, nil)
				Expect(status).To(Equal(http.StatusNotFound), "%s %s: %s", route.method, route.path, body)
			}
			return
		}

		address := "Invitee-" + randSuffix(4) + "@Example.com"
		var invite signupInviteJSON
		status, body := api.doRaw(http.MethodPost, "/admin/signup-invites", map[string]any{"email": " " + address + " ", "expires_in_days": 7})
		Expect(status).To(Equal(http.StatusCreated), "%s", body)
		decodeInto(body, &invite)
		Expect(invite.ID).NotTo(BeEmpty())
		Expect(invite.Email).To(Equal(strings.ToLower(address)), "addresses are normalized")
		Expect(invite.InvitedBy).To(Equal(api.me()))
		Expect(invite.ExpiresAt).NotTo(BeNil())
		Expect(invite.ExpiresAt.Sub(invite.CreatedAt)).To(BeNumerically("~", 7*24*time.Hour, time.Minute))

		forever := "forever-" + randSuffix(4) + "@example.com"
		status, body = api.doRaw(http.MethodPost, "/admin/signup-invites", map[string]any{"email": forever})
		Expect(status).To(Equal(http.StatusCreated), "%s", body)
		var open signupInviteJSON
		decodeInto(body, &open)
		Expect(open.ExpiresAt).To(BeNil(), "no expiry by default")

		list := api.signupInvites()
		Expect(findInvite(list, strings.ToLower(address))).NotTo(BeNil())
		Expect(findInvite(list, forever)).NotTo(BeNil())

		for _, bad := range []map[string]any{
			{"email": "not-an-address"},
			{"email": "Name <x@example.com>"},
			{"email": "ok-" + randSuffix(3) + "@example.com", "expires_in_days": 366},
			{"email": "ok-" + randSuffix(3) + "@example.com", "expires_in_days": -1},
		} {
			status, body := api.doRaw(http.MethodPost, "/admin/signup-invites", bad)
			Expect(status).To(Equal(http.StatusBadRequest), "%v: %s", bad, body)
		}

		admin := newIdentity("invites-admin").promote()
		adminAPI := newAPIClient(cli.apiURL, admin.email, admin.password)
		status, _ = adminAPI.doRaw(http.MethodGet, "/admin/signup-invites", nil)
		Expect(status).To(Equal(http.StatusForbidden), "a global admin is not a super admin")
		status, _ = adminAPI.doRaw(http.MethodPost, "/admin/signup-invites", map[string]any{"email": "x-" + randSuffix(3) + "@example.com"})
		Expect(status).To(Equal(http.StatusForbidden))
		status, _ = adminAPI.doRaw(http.MethodDelete, "/admin/signup-invites/"+invite.ID, nil)
		Expect(status).To(Equal(http.StatusForbidden))

		status, body = api.doRaw(http.MethodDelete, "/admin/signup-invites/"+invite.ID, nil)
		Expect(status).To(Equal(http.StatusNoContent), "%s", body)
		Expect(findInvite(api.signupInvites(), strings.ToLower(address))).To(BeNil())
		status, _ = api.doRaw(http.MethodDelete, "/admin/signup-invites/"+invite.ID, nil)
		Expect(status).To(Equal(http.StatusNotFound))
		status, _ = api.doRaw(http.MethodDelete, "/admin/signup-invites/not-an-id!", nil)
		Expect(status).To(Equal(http.StatusBadRequest))
	})

	It("guards the waitlist admin routes and validates their parameters", func() {
		saasEnabled()
		if cli.edition == "free" {
			for _, route := range []struct{ method, path string }{
				{http.MethodGet, "/admin/waitlist"},
				{http.MethodPost, "/admin/waitlist/notify"},
				{http.MethodDelete, "/admin/waitlist/1"},
			} {
				status, body := api.doRaw(route.method, route.path, nil)
				Expect(status).To(Equal(http.StatusNotFound), "%s %s: %s", route.method, route.path, body)
			}
			return
		}

		status, body := api.doRaw(http.MethodGet, "/admin/waitlist?pending=true&limit=5", nil)
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		for _, query := range []string{"?pending=maybe", "?limit=0", "?limit=abc", "?limit=1001", "?after=not-an-id!"} {
			status, body := api.doRaw(http.MethodGet, "/admin/waitlist"+query, nil)
			Expect(status).To(Equal(http.StatusBadRequest), "%s: %s", query, body)
		}
		status, body = api.doRaw(http.MethodPost, "/admin/waitlist/notify", map[string]int{"limit": 1001})
		Expect(status).To(Equal(http.StatusBadRequest), "%s", body)
		status, body = api.doRaw(http.MethodDelete, "/admin/waitlist/zzzzzzzzzz", nil)
		Expect(status).To(Equal(http.StatusNotFound), "%s", body)
		status, body = api.doRaw(http.MethodDelete, "/admin/waitlist/not-an-id!", nil)
		Expect(status).To(Equal(http.StatusBadRequest), "%s", body)

		admin := newIdentity("waitlist-admin").promote()
		adminAPI := newAPIClient(cli.apiURL, admin.email, admin.password)
		status, _ = adminAPI.doRaw(http.MethodGet, "/admin/waitlist", nil)
		Expect(status).To(Equal(http.StatusForbidden))
		status, _ = adminAPI.doRaw(http.MethodPost, "/admin/waitlist/notify", map[string]int{"limit": 1})
		Expect(status).To(Equal(http.StatusForbidden))
		status, _ = adminAPI.doRaw(http.MethodDelete, "/admin/waitlist/zzzzzzzzzz", nil)
		Expect(status).To(Equal(http.StatusForbidden))
	})

	It("reports terms as not required when no terms version is published", func() {
		saasEnabled()
		if cli.edition == "free" {
			status, _ := api.doRaw(http.MethodGet, "/me/terms", nil)
			Expect(status).To(Equal(http.StatusNotFound))
			status, _ = api.doRaw(http.MethodPost, "/me/terms", map[string]string{"version": "1"})
			Expect(status).To(Equal(http.StatusNotFound))
			return
		}

		var terms termsStatusJSON
		status, body := api.doRaw(http.MethodGet, "/me/terms", nil)
		Expect(status).To(Equal(http.StatusOK), "%s", body)
		decodeInto(body, &terms)
		Expect(terms).To(Equal(termsStatusJSON{}), "no version, nothing accepted, nothing required")

		status, body = api.doRaw(http.MethodPost, "/me/terms", map[string]string{"version": "2026-01-01"})
		Expect(status).To(Equal(http.StatusNotFound), "terms acceptance is not enabled: %s", body)

		status, _ = api.doAnonRaw(http.MethodGet, "/me/terms", nil)
		Expect(status).To(Equal(http.StatusUnauthorized))
	})

	It("rejects social login flows when no provider is configured", func() {
		saasEnabled()
		root := newAPIClientWithToken(cli.apiURL, "")
		if cli.edition == "free" {
			status, _ := root.doAnonRaw(http.MethodGet, "/auth/oauth/github/start", nil)
			Expect(status).To(Equal(http.StatusNotFound))
			status, _ = root.doAnonRaw(http.MethodGet, "/auth/oauth/github/callback?code=c&state=s", nil)
			Expect(status).To(Equal(http.StatusNotFound))
			status, _ = root.doAnonRaw(http.MethodPost, "/auth/oauth/complete", map[string]string{"code": "c"})
			Expect(status).To(Equal(http.StatusNotFound))
			return
		}

		for _, provider := range []string{"github", "google", "unknown"} {
			status, body := root.doAnonRaw(http.MethodGet, "/auth/oauth/"+provider+"/start?redirect=/", nil)
			Expect(status).To(Equal(http.StatusNotFound), "%s start: %s", provider, body)
			Expect(string(body)).To(ContainSubstring("oauth provider is not enabled"))
			status, body = root.doAnonRaw(http.MethodGet, "/auth/oauth/"+provider+"/callback?code=c&state=s", nil)
			Expect(status).To(Equal(http.StatusNotFound), "%s callback: %s", provider, body)
		}

		status, body := root.doAnonRaw(http.MethodPost, "/auth/oauth/complete", map[string]string{"code": ""})
		Expect(status).To(Equal(http.StatusBadRequest), "%s", body)
		Expect(string(body)).To(ContainSubstring("login code is required"))
		status, body = root.doAnonRaw(http.MethodPost, "/auth/oauth/complete", map[string]string{"code": "forged-" + randSuffix(8)})
		Expect(status).To(Equal(http.StatusNotFound), "%s", body)
		Expect(string(body)).To(ContainSubstring("invalid or expired login code"))

		status, body = api.doRaw(http.MethodGet, "/me/identities/google/start", nil)
		Expect(status).To(Equal(http.StatusNotFound), "%s", body)
		status, body = api.doRaw(http.MethodDelete, "/me/identities/not-an-id!", nil)
		Expect(status).To(Equal(http.StatusBadRequest), "%s", body)
		status, _ = api.doAnonRaw(http.MethodGet, "/me/identities", nil)
		Expect(status).To(Equal(http.StatusUnauthorized))
	})

	Context("on an invite-only server with published terms", func() {
		It("puts uninvited sign-ups on the waitlist and lets them in once notified", func() {
			saasEnabled()
			if cli.edition == "free" {
				return // the routes' absence is asserted by the specs above
			}
			admin, termsVersion := inviteOnlyServer()
			Expect(termsVersion).NotTo(BeEmpty())

			var caps inviteOnlyCapabilitiesJSON
			status, body := admin.doAnonRaw(http.MethodGet, "/auth/capabilities", nil)
			Expect(status).To(Equal(http.StatusOK), "%s", body)
			decodeInto(body, &caps)
			Expect(caps.Registration).To(BeTrue())
			Expect(caps.InviteOnly).To(BeTrue())
			Expect(caps.Terms).NotTo(BeNil())
			Expect(caps.Terms.Version).To(Equal(termsVersion))

			address := "waitlisted-" + randSuffix(6) + "@example.com"
			status, body = admin.doAnonRaw(http.MethodPost, "/auth/register", map[string]any{"name": "Wait List", "email": address})
			Expect(status).To(Equal(http.StatusBadRequest), "the terms must be accepted: %s", body)
			Expect(string(body)).To(ContainSubstring("accept the terms"))

			register := func() {
				GinkgoHelper()
				status, body := admin.doAnonRaw(http.MethodPost, "/auth/register", map[string]any{"name": "Wait List", "email": address, "accept_terms": true})
				Expect(status).To(Equal(http.StatusAccepted), "an uninvited sign-up looks like any other: %s", body)
			}
			register()
			joined := email.waitForSubject(address, "You're on the Nipa waitlist")
			Expect(joined.Text).To(ContainSubstring(address))
			register()

			pending := admin.waitlist("?pending=true&limit=1000")
			entry := findWaitlistEntry(pending, address)
			Expect(entry).NotTo(BeNil(), "the address joined the waitlist")
			Expect(entry.Name).To(Equal("Wait List"))
			Expect(entry.Source).To(Equal("registration"))
			Expect(entry.NotifiedAt).To(BeNil())
			Expect(email.countSubject(address, "You're on the Nipa waitlist")).To(Equal(1), "joining twice changes nothing")
			Expect(email.countSubject(address, "Confirm your email address")).To(BeZero(), "no account was created")

			status, body = admin.doAnonRaw(http.MethodPost, "/auth/login", map[string]string{"email": address, "password": "waitlist-password-123"})
			Expect(status).To(BeNumerically(">=", 400), "%s", body)

			var notified struct {
				Notified int `json:"notified"`
			}
			status, body = admin.doRaw(http.MethodPost, "/admin/waitlist/notify", map[string]int{"limit": 1000})
			Expect(status).To(Equal(http.StatusOK), "%s", body)
			decodeInto(body, &notified)
			Expect(notified.Notified).To(BeNumerically(">=", 1))
			open := email.waitForSubject(address, "You can now create your Nipa account")
			Expect(open.Text).To(ContainSubstring("/register"))

			Expect(findWaitlistEntry(admin.waitlist("?pending=true&limit=1000"), address)).To(BeNil())
			done := findWaitlistEntry(admin.waitlist("?pending=false&limit=1000"), address)
			Expect(done).NotTo(BeNil())
			Expect(done.NotifiedAt).NotTo(BeNil())
			Expect(findInvite(admin.signupInvites(), address)).NotTo(BeNil(), "notifying allowlists the address while sign-up is invite-only")

			status, body = admin.doRaw(http.MethodPost, "/admin/waitlist/notify", nil)
			Expect(status).To(Equal(http.StatusOK), "%s", body)
			decodeInto(body, &notified)
			Expect(notified.Notified).To(BeZero(), "nobody is left to notify")

			register()
			user := registerAndVerify(admin, address, "waitlist-password-123")
			var terms termsStatusJSON
			user.do(http.MethodGet, "/me/terms", nil, &terms, true)
			Expect(terms.Version).To(Equal(termsVersion))
			Expect(terms.AcceptedVersion).To(Equal(termsVersion), "accepting at sign-up is recorded")
			Expect(terms.AcceptedAt).NotTo(BeEmpty())
			Expect(terms.Required).To(BeFalse())

			// Removing an entry takes it off the list.
			gone := "removed-" + randSuffix(6) + "@example.com"
			status, body = admin.doAnonRaw(http.MethodPost, "/auth/register", map[string]any{"name": "Gone", "email": gone, "accept_terms": true})
			Expect(status).To(Equal(http.StatusAccepted), "%s", body)
			removed := findWaitlistEntry(admin.waitlist("?limit=1000"), gone)
			Expect(removed).NotTo(BeNil())
			status, body = admin.doRaw(http.MethodDelete, "/admin/waitlist/"+removed.ID, nil)
			Expect(status).To(Equal(http.StatusNoContent), "%s", body)
			Expect(findWaitlistEntry(admin.waitlist("?limit=1000"), gone)).To(BeNil())
			status, _ = admin.doRaw(http.MethodDelete, "/admin/waitlist/"+removed.ID, nil)
			Expect(status).To(Equal(http.StatusNotFound))
		})

		It("lets an invited address sign up directly", func() {
			saasEnabled()
			if cli.edition == "free" {
				return // the routes' absence is asserted by the specs above
			}
			admin, _ := inviteOnlyServer()

			address := "invited-" + randSuffix(6) + "@example.com"
			status, body := admin.doRaw(http.MethodPost, "/admin/signup-invites", map[string]any{"email": address, "expires_in_days": 1})
			Expect(status).To(Equal(http.StatusCreated), "%s", body)

			status, body = admin.doAnonRaw(http.MethodPost, "/auth/register", map[string]any{"name": "Invited", "email": address, "accept_terms": true})
			Expect(status).To(Equal(http.StatusAccepted), "%s", body)
			registerAndVerify(admin, address, "invited-password-123")
			Expect(email.countSubject(address, "You're on the Nipa waitlist")).To(BeZero())
			Expect(findWaitlistEntry(admin.waitlist("?limit=1000"), address)).To(BeNil())
		})

		It("asks existing accounts to accept the current terms", func() {
			saasEnabled()
			if cli.edition == "free" {
				return // the routes' absence is asserted by the specs above
			}
			admin, termsVersion := inviteOnlyServer()

			var terms termsStatusJSON
			admin.do(http.MethodGet, "/me/terms", nil, &terms, true)
			if terms.AcceptedVersion == "" {
				Expect(terms.Required).To(BeTrue(), "the seeded admin never accepted: %+v", terms)
			}
			Expect(terms.Version).To(Equal(termsVersion))

			status, body := admin.doRaw(http.MethodPost, "/me/terms", map[string]string{"version": "1999-01-01"})
			Expect(status).To(Equal(http.StatusConflict), "an outdated version is refused: %s", body)

			status, body = admin.doRaw(http.MethodPost, "/me/terms", map[string]string{"version": " " + termsVersion + " "})
			Expect(status).To(Equal(http.StatusOK), "%s", body)
			decodeInto(body, &terms)
			Expect(terms.AcceptedVersion).To(Equal(termsVersion))
			Expect(terms.Required).To(BeFalse())
			accepted, err := time.Parse(time.RFC3339, terms.AcceptedAt)
			Expect(err).NotTo(HaveOccurred())
			Expect(accepted).To(BeTemporally("~", time.Now(), 5*time.Minute))
		})
	})
})
