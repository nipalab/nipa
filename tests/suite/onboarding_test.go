package suite_test

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func saasEnabled() {
	if os.Getenv("NIPA_TEST_SAAS") != "1" {
		Skip("saas onboarding is not enabled")
	}
}

type saasCapabilities struct {
	Registration bool     `json:"registration"`
	Providers    []string `json:"providers"`
}

func saasCapabilitiesProbe() (int, saasCapabilities) {
	var out saasCapabilities
	status, body := api.doAnonRaw(http.MethodGet, "/auth/capabilities", nil)
	if status == http.StatusOK {
		Expect(json.Unmarshal(body, &out)).To(Succeed())
	}
	return status, out
}

var verificationTokenPattern = regexp.MustCompile(`token=([A-Za-z0-9_-]{16,})`)

// verificationToken extracts the one-time token from a confirmation email.
func verificationToken(mail receivedEmail) string {
	GinkgoHelper()

	match := verificationTokenPattern.FindStringSubmatch(mail.Text + " " + mail.HTML)
	Expect(match).To(HaveLen(2), "no token in confirmation email: %q", mail.Text)
	return match[1]
}

// waitForConfirmations returns the confirmation emails for one address once at
// least count of them arrived (resends produce additional messages).
func waitForConfirmations(address string, count int) []receivedEmail {
	GinkgoHelper()

	var found []receivedEmail
	Eventually(func() bool {
		found = nil
		for _, mail := range email.all() {
			if contains(mail.To, address) && strings.Contains(mail.Subject, "Confirm your email address") {
				found = append(found, mail)
			}
		}
		return len(found) >= count
	}, "15s", "50ms").Should(BeTrue(), "expected %d confirmation emails for %s; got %v", count, address, email.subjects())
	return found
}

var _ = Describe("saas onboarding", func() {
	It("registers an account gated behind email confirmation", func() {
		saasEnabled()

		if cli.edition == "free" {
			status, _ := saasCapabilitiesProbe()
			Expect(status).To(Equal(http.StatusNotFound), "the free edition wires no onboarding routes")
			status, _ = api.doAnonRaw(http.MethodPost, "/auth/register", map[string]string{
				"name": "Free User", "email": "free-" + randSuffix(4) + "@example.com", "password": "saas-password-123",
			})
			Expect(status).To(Equal(http.StatusNotFound), "registration is enterprise-only")
			return
		}

		status, capabilities := saasCapabilitiesProbe()
		Expect(status).To(Equal(http.StatusOK))
		Expect(capabilities.Registration).To(BeTrue())
		Expect(capabilities.Providers).To(BeEmpty(), "the harness configures no OAuth credentials")

		address := "saas-" + randSuffix(6) + "@example.com"
		const password = "saas-password-123"

		status, body := api.doAnonRaw(http.MethodPost, "/auth/register", map[string]string{
			"name": "SaaS User", "email": address, "password": password,
		})
		Expect(status).To(Equal(http.StatusAccepted), "register: %s", body)
		Expect(string(body)).To(ContainSubstring("pending_verification"))

		status, body = api.doAnonRaw(http.MethodPost, "/auth/register", map[string]string{
			"name": "SaaS User", "email": address, "password": password,
		})
		Expect(status).To(Equal(http.StatusConflict), "duplicate email: %s", body)

		status, body = api.doAnonRaw(http.MethodPost, "/auth/register", map[string]string{
			"name": "SaaS User", "email": "weak-" + randSuffix(4) + "@example.com", "password": "short",
		})
		Expect(status).To(Equal(http.StatusBadRequest), "weak password: %s", body)

		status, body = api.doAnonRaw(http.MethodPost, "/auth/login", map[string]string{
			"email": address, "password": password,
		})
		Expect(status).To(Equal(http.StatusForbidden), "unconfirmed account cannot log in: %s", body)
		Expect(string(body)).To(ContainSubstring("email not confirmed"))

		confirmation := email.waitForSubject(address, "Confirm your email address")
		Expect(confirmation.From).NotTo(BeEmpty())
		token := verificationToken(confirmation)

		status, body = api.doAnonRaw(http.MethodPost, "/auth/verify", map[string]string{"token": token})
		Expect(status).To(Equal(http.StatusOK), "verify: %s", body)
		var session struct {
			AccessToken string `json:"access_token"`
		}
		Expect(json.Unmarshal(body, &session)).To(Succeed())
		Expect(session.AccessToken).NotTo(BeEmpty(), "verify must start a session")

		confirmed := newAPIClientWithToken(cli.apiURL, session.AccessToken)
		var me struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		}
		confirmed.do(http.MethodGet, "/me", nil, &me, true)
		Expect(me.Email).To(Equal(address))
		Expect(me.ID).NotTo(BeEmpty())

		status, body = api.doAnonRaw(http.MethodPost, "/auth/verify", map[string]string{"token": token})
		Expect(status).To(Equal(http.StatusNotFound), "the confirmation token is single use: %s", body)

		// The gRPC login path the CLI uses accepts the confirmed account.
		result := loginResult(cli.host, address, password)
		Expect(result.AccessToken).NotTo(BeEmpty(), "after confirmation the account logs in")
	})

	It("resends the confirmation email and invalidates the old token", func() {
		saasEnabled()

		if cli.edition == "free" {
			return
		}

		address := "saas-resend-" + randSuffix(6) + "@example.com"
		status, body := api.doAnonRaw(http.MethodPost, "/auth/register", map[string]string{
			"name": "Resend User", "email": address, "password": "saas-password-123",
		})
		Expect(status).To(Equal(http.StatusAccepted), "register: %s", body)

		firstToken := verificationToken(email.waitForSubject(address, "Confirm your email address"))

		status, body = api.doAnonRaw(http.MethodPost, "/auth/register/resend", map[string]string{"email": address})
		Expect(status).To(Equal(http.StatusOK), "resend: %s", body)

		confirmations := waitForConfirmations(address, 2)
		secondToken := verificationToken(confirmations[len(confirmations)-1])
		Expect(secondToken).NotTo(Equal(firstToken))

		status, _ = api.doAnonRaw(http.MethodPost, "/auth/verify", map[string]string{"token": firstToken})
		Expect(status).To(Equal(http.StatusNotFound), "resending voids the previous token")

		status, body = api.doAnonRaw(http.MethodPost, "/auth/verify", map[string]string{"token": secondToken})
		Expect(status).To(Equal(http.StatusOK), "the resent token works: %s", body)

		status, _ = api.doAnonRaw(http.MethodPost, "/auth/register/resend", map[string]string{
			"email": "nobody-" + randSuffix(4) + "@example.com",
		})
		Expect(status).To(Equal(http.StatusOK), "unknown addresses are accepted so the endpoint cannot probe")
	})

	It("serves the identity routes with no providers configured", func() {
		saasEnabled()

		if cli.edition == "free" {
			status, _ := api.doAnonRaw(http.MethodGet, "/me/identities", nil)
			Expect(status).To(Equal(http.StatusNotFound), "the free edition wires no identity routes")
			return
		}

		status, body := api.doRaw(http.MethodGet, "/me/identities", nil)
		Expect(status).To(Equal(http.StatusOK), "identities: %s", body)
		Expect(strings.TrimSpace(string(body))).To(Equal("[]"))

		status, body = api.doRaw(http.MethodGet, "/me/identities/github/start", nil)
		Expect(status).To(Equal(http.StatusNotFound), "unconfigured providers cannot be linked: %s", body)

		status, _ = api.doRaw(http.MethodDelete, "/me/identities/1", nil)
		Expect(status).To(Equal(http.StatusNotFound))
	})
})
