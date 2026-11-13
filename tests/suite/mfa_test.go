package suite_test

import (
	"encoding/base32"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nipalab/nipa/internal/totp"
)

type mfaStatusJSON struct {
	Available bool `json:"available"`
	TOTP      struct {
		Enabled bool `json:"enabled"`
	} `json:"totp"`
	RecoveryCodesLeft int64 `json:"recovery_codes_left"`
}

type mfaLoginJSON struct {
	AccessToken  string `json:"access_token"`
	MFARequired  bool   `json:"mfa_required"`
	MFAChallenge string `json:"mfa_challenge"`
	ExpiresIn    int    `json:"expires_in"`
}

// totpCodes hands out codes for strictly increasing time steps, so the
// server's replay guard (a code at or below the last accepted step is
// refused) never rejects a code the spec expects to work.
type totpCodes struct {
	secret []byte
	last   int64
}

func (c *totpCodes) next() string {
	for {
		current := totp.Step(time.Now())
		step := max(current, c.last+1)
		if step <= current+1 {
			c.last = step
			return totp.Code(c.secret, step)
		}
		time.Sleep(time.Until(time.Unix((current+1)*totp.Period, 0)) + 100*time.Millisecond)
	}
}

func mfaPasswordLogin(email, password string) mfaLoginJSON {
	GinkgoHelper()
	status, body := api.doAnonRaw(http.MethodPost, "/auth/login", map[string]string{"email": email, "password": password})
	Expect(status).To(Equal(http.StatusOK), string(body))
	var out mfaLoginJSON
	Expect(json.Unmarshal(body, &out)).To(Succeed())
	return out
}

func mfaVerify(challenge, code string) (int, mfaLoginJSON, string) {
	GinkgoHelper()
	status, body := api.doAnonRaw(http.MethodPost, "/auth/mfa/verify", map[string]string{"challenge": challenge, "code": code})
	var out mfaLoginJSON
	if status == http.StatusOK {
		Expect(json.Unmarshal(body, &out)).To(Succeed())
	}
	return status, out, string(body)
}

var _ = Describe("two-factor authentication", func() {
	It("enrolls, logs in with a code and a recovery code, refuses a replay and disables", func() {
		email := fmt.Sprintf("mfa-%d-%s@example.com", time.Now().UnixNano(), randSuffix(3))
		password := "mfa-password-123"
		api.createUser("mfa user", email, password)
		user := newAPIClient(cli.apiURL, email, password)

		var status mfaStatusJSON
		user.do(http.MethodGet, "/me/mfa", nil, &status, true)
		if !status.Available {
			Skip("two-factor authentication is not available on this server (MFA_ENABLED/MFA_ENCRYPTION_KEY)")
		}
		Expect(status.TOTP.Enabled).To(BeFalse())

		code, _ := user.doRaw(http.MethodPost, "/me/mfa/totp", map[string]string{"password": "wrong-password"})
		Expect(code).To(Equal(http.StatusBadRequest))

		var enrollment struct {
			Secret string `json:"secret"`
			URI    string `json:"uri"`
		}
		user.do(http.MethodPost, "/me/mfa/totp", map[string]string{"password": password}, &enrollment, true)
		Expect(enrollment.URI).To(HavePrefix("otpauth://totp/"))
		secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret)
		Expect(err).NotTo(HaveOccurred())
		codes := &totpCodes{secret: secret}

		Expect(mfaPasswordLogin(email, password).MFARequired).To(BeFalse(), "an unconfirmed secret never gates a login")

		var recovery struct {
			RecoveryCodes []string `json:"recovery_codes"`
		}
		user.do(http.MethodPost, "/me/mfa/totp/confirm", map[string]string{"code": codes.next()}, &recovery, true)
		Expect(recovery.RecoveryCodes).To(HaveLen(10))

		user.do(http.MethodGet, "/me/mfa", nil, &status, true)
		Expect(status.TOTP.Enabled).To(BeTrue())
		Expect(status.RecoveryCodesLeft).To(BeEquivalentTo(10))

		first := mfaPasswordLogin(email, password)
		Expect(first.MFARequired).To(BeTrue())
		Expect(first.AccessToken).To(BeEmpty())
		Expect(first.MFAChallenge).NotTo(BeEmpty())
		Expect(first.ExpiresIn).To(BeNumerically(">", 0))

		wrong, _, body := mfaVerify(first.MFAChallenge, "000000")
		Expect(wrong).To(Equal(http.StatusBadRequest))
		Expect(body).To(ContainSubstring("invalid code"))

		used := codes.next()
		ok, session, body := mfaVerify(first.MFAChallenge, used)
		Expect(ok).To(Equal(http.StatusOK), body)
		Expect(session.AccessToken).NotTo(BeEmpty())
		Expect(newAPIClientWithToken(cli.apiURL, session.AccessToken).me()).NotTo(BeEmpty())

		again, _, _ := mfaVerify(first.MFAChallenge, used)
		Expect(again).To(Equal(http.StatusBadRequest), "a spent challenge is gone")

		replay := mfaPasswordLogin(email, password)
		Expect(replay.MFARequired).To(BeTrue())
		refused, _, body := mfaVerify(replay.MFAChallenge, used)
		Expect(refused).To(Equal(http.StatusBadRequest), "a code is single-use: %s", body)

		home := workspace()
		tokenFile := filepath.Join(home, "tokens.json")
		run := startNipaPTY(home, append(isolatedSSHEnv(home, tokenFile), "NIPA_OTP="),
			"login", "--host", cli.host, "--username", email, "--otp", codes.next())
		Eventually(run.out.String, 20*time.Second, 50*time.Millisecond).Should(ContainSubstring("Password:"))
		_, err = run.master.WriteString(password + "\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(run.wait(30 * time.Second)).To(ContainSubstring("Logged in to " + cli.host + " as " + email))
		stored, err := os.ReadFile(tokenFile)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(ContainSubstring(email), "the CLI stored the session")

		recoveryLogin := mfaPasswordLogin(email, password)
		Expect(recoveryLogin.MFARequired).To(BeTrue())
		ok, session, body = mfaVerify(recoveryLogin.MFAChallenge, strings.ToLower(recovery.RecoveryCodes[0]))
		Expect(ok).To(Equal(http.StatusOK), body)
		Expect(session.AccessToken).NotTo(BeEmpty())

		reuse := mfaPasswordLogin(email, password)
		refused, _, _ = mfaVerify(reuse.MFAChallenge, recovery.RecoveryCodes[0])
		Expect(refused).To(Equal(http.StatusBadRequest), "a recovery code is single-use")

		user.do(http.MethodGet, "/me/mfa", nil, &status, true)
		Expect(status.RecoveryCodesLeft).To(BeEquivalentTo(9))

		denied, _ := user.doRaw(http.MethodDelete, "/me/mfa/totp", map[string]string{"code": "000000"})
		Expect(denied).To(Equal(http.StatusBadRequest))
		user.do(http.MethodDelete, "/me/mfa/totp", map[string]string{"code": recovery.RecoveryCodes[1]}, nil, true)

		user.do(http.MethodGet, "/me/mfa", nil, &status, true)
		Expect(status.TOTP.Enabled).To(BeFalse())
		Expect(status.RecoveryCodesLeft).To(BeZero())
		after := mfaPasswordLogin(email, password)
		Expect(after.MFARequired).To(BeFalse())
		Expect(after.AccessToken).NotTo(BeEmpty())
	})
})
