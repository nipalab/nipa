package suite_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("profile settings", func() {
	It("toggles email notifications on the authenticated profile", func() {
		var profile struct {
			Name        string `json:"name"`
			NotifyEmail bool   `json:"notify_email"`
		}
		Expect(api.do(http.MethodGet, "/me", nil, &profile, true)).To(Equal(http.StatusOK))
		Expect(profile.Name).NotTo(BeEmpty())
		Expect(profile.NotifyEmail).To(BeTrue(), "notifications default to enabled")

		Expect(api.do(http.MethodPatch, "/me", map[string]any{
			"name":         profile.Name,
			"notify_email": false,
		}, &profile, true)).To(Equal(http.StatusOK))
		Expect(profile.NotifyEmail).To(BeFalse())

		var reread struct {
			NotifyEmail bool `json:"notify_email"`
		}
		Expect(api.do(http.MethodGet, "/me", nil, &reread, true)).To(Equal(http.StatusOK))
		Expect(reread.NotifyEmail).To(BeFalse())

		Expect(api.do(http.MethodPatch, "/me", map[string]any{
			"name":         profile.Name,
			"notify_email": true,
		}, &reread, true)).To(Equal(http.StatusOK))
		Expect(reread.NotifyEmail).To(BeTrue())
	})
})
