package suite_test

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
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

	It("ends a challenge after five wrong codes and audits every failure", func() {
		acct := newMFAAccount("mfa-attempts")
		acct.enroll()

		login := mfaPasswordLogin(acct.email, acct.password)
		Expect(login.MFARequired).To(BeTrue())
		for attempt := range 5 {
			status, _, body := mfaVerify(login.MFAChallenge, "000000")
			Expect(status).To(Equal(http.StatusBadRequest), "attempt %d: %s", attempt, body)
		}
		status, _, _ := mfaVerify(login.MFAChallenge, acct.recovery[0])
		Expect(status).To(Equal(http.StatusBadRequest), "the fifth wrong code ended the challenge")

		retry := mfaPasswordLogin(acct.email, acct.password)
		status, session, body := mfaVerify(retry.MFAChallenge, acct.nextRecovery())
		Expect(status).To(Equal(http.StatusOK), body)
		Expect(session.AccessToken).NotTo(BeEmpty())

		auditStatus, failed := listAudit(api, url.Values{"action": {"auth.login.failed"}, "target": {acct.id}})
		Expect(auditStatus).To(Equal(http.StatusOK))
		Expect(failed.Entries).To(HaveLen(5))
		for _, entry := range failed.Entries {
			Expect(entry.ActorType).To(Equal("system"))
			Expect(entry.Metadata).To(Equal(map[string]any{
				"method": "password", "email": acct.email, "reason": "mfa_code",
			}))
		}

		auditStatus, succeeded := listAudit(api, url.Values{"action": {"auth.login.succeeded"}, "target": {acct.id}, "limit": {"1"}})
		Expect(auditStatus).To(Equal(http.StatusOK))
		Expect(succeeded.Entries).To(HaveLen(1))
		Expect(succeeded.Entries[0].Metadata).To(HaveKeyWithValue("mfa", "recovery_code"))

		auditStatus, enabled := listAudit(api, url.Values{"action": {"user.mfa."}, "target": {acct.id}})
		Expect(auditStatus).To(Equal(http.StatusOK))
		Expect(auditActions(enabled.Entries)).To(Equal([]string{"user.mfa.enabled"}))
		Expect(enabled.Entries[0].ActorUserID).To(Equal(acct.id))
		Expect(enabled.Entries[0].Metadata).To(HaveKeyWithValue("method", "totp"))
	})

	It("regenerates recovery codes and mails the owner about second-factor changes", func() {
		acct := newMFAAccount("mfa-regen")
		acct.enroll()
		email.waitForSubject(acct.email, "Two-factor authentication was turned on")

		status, _ := acct.api.doRaw(http.MethodPost, "/me/mfa/recovery-codes", map[string]string{"code": "000000"})
		Expect(status).To(Equal(http.StatusBadRequest))

		old := acct.recovery
		var fresh struct {
			RecoveryCodes []string `json:"recovery_codes"`
		}
		acct.api.do(http.MethodPost, "/me/mfa/recovery-codes", map[string]string{"code": old[0]}, &fresh, true)
		Expect(fresh.RecoveryCodes).To(HaveLen(10))
		Expect(fresh.RecoveryCodes).NotTo(ContainElements(old[1], old[2]))

		var mfaStatus mfaStatusJSON
		acct.api.do(http.MethodGet, "/me/mfa", nil, &mfaStatus, true)
		Expect(mfaStatus.RecoveryCodesLeft).To(BeEquivalentTo(10))

		login := mfaPasswordLogin(acct.email, acct.password)
		refused, _, _ := mfaVerify(login.MFAChallenge, old[1])
		Expect(refused).To(Equal(http.StatusBadRequest), "regenerating replaces every old code")
		ok, session, body := mfaVerify(login.MFAChallenge, fresh.RecoveryCodes[0])
		Expect(ok).To(Equal(http.StatusOK), body)
		Expect(session.AccessToken).NotTo(BeEmpty())

		Eventually(func() int {
			return email.countSubject(acct.email, "A recovery code was used")
		}, "15s", "50ms").Should(Equal(2), "regenerating with a recovery code and signing in with one both notify")

		auditStatus, entries := listAudit(api, url.Values{"action": {"user.mfa."}, "target": {acct.id}})
		Expect(auditStatus).To(Equal(http.StatusOK))
		Expect(auditActions(entries.Entries)).To(Equal([]string{"user.mfa.recovery_codes_regenerated", "user.mfa.enabled"}))
	})

	It("lets an administrator reset another account's second factor", func() {
		acct := newMFAAccount("mfa-reset")
		acct.enroll()

		bystander := newIdentity("mfa-bystander")
		status, _ := newAPIClient(cli.apiURL, bystander.email, bystander.password).
			doRaw(http.MethodDelete, "/users/"+acct.id+"/mfa", nil)
		Expect(status).To(Equal(http.StatusForbidden))

		api.do(http.MethodDelete, "/users/"+acct.id+"/mfa", nil, nil, true)

		var mfaStatus mfaStatusJSON
		acct.api.do(http.MethodGet, "/me/mfa", nil, &mfaStatus, true)
		Expect(mfaStatus.TOTP.Enabled).To(BeFalse())
		Expect(mfaStatus.RecoveryCodesLeft).To(BeZero())
		after := mfaPasswordLogin(acct.email, acct.password)
		Expect(after.MFARequired).To(BeFalse())
		Expect(after.AccessToken).NotTo(BeEmpty())

		status, body := api.doRaw(http.MethodDelete, "/users/"+acct.id+"/mfa", nil)
		Expect(status).To(Equal(http.StatusBadRequest))
		Expect(string(body)).To(ContainSubstring("not enabled"))

		email.waitForSubject(acct.email, "An administrator turned off two-factor authentication")

		auditStatus, reset := listAudit(api, url.Values{"action": {"user.mfa.reset_by_admin"}, "target": {acct.id}})
		Expect(auditStatus).To(Equal(http.StatusOK))
		Expect(reset.Entries).To(HaveLen(1))
		Expect(reset.Entries[0].ActorUserID).To(Equal(api.me()))
	})

	It("keeps SSH key and refresh-token logins free of a code and refuses older gRPC clients", func() {
		acct := newMFAAccount("mfa-grpc")
		acct.enroll()

		conn, err := grpc.NewClient(cli.host, grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close() })
		svc := pb.NewNipaServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		DeferCleanup(cancel)

		_, err = svc.LoginWithUsernamePassword(ctx, &pb.LoginUsernamePasswordRequest{Username: acct.email, Password: acct.password})
		Expect(status.Code(err)).To(Equal(codes.FailedPrecondition), "%v", err)
		Expect(err.Error()).To(ContainSubstring("two-factor"))

		started, err := svc.LoginWithUsernamePassword(ctx, &pb.LoginUsernamePasswordRequest{
			Username: acct.email, Password: acct.password, SupportsMfa: true,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(started.GetAccessToken()).To(BeEmpty())
		Expect(started.GetMfaChallenge()).NotTo(BeEmpty())
		Expect(started.GetExpiresIn()).To(BeNumerically(">", 0))

		_, err = svc.CompleteLoginMFA(ctx, &pb.CompleteLoginMFARequest{Challenge: started.GetMfaChallenge(), Code: "000000"})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument), "%v", err)
		done, err := svc.CompleteLoginMFA(ctx, &pb.CompleteLoginMFARequest{Challenge: started.GetMfaChallenge(), Code: acct.nextRecovery()})
		Expect(err).NotTo(HaveOccurred())
		Expect(done.GetAccessToken()).NotTo(BeEmpty())
		Expect(done.GetRefreshToken()).NotTo(BeEmpty())

		refreshed, err := svc.LoginWithRefreshToken(ctx, &pb.LoginWithRefreshRequest{RefreshToken: done.GetRefreshToken()})
		Expect(err).NotTo(HaveOccurred())
		Expect(refreshed.GetMfaChallenge()).To(BeEmpty())
		Expect(newAPIClientWithToken(cli.apiURL, refreshed.GetAccessToken()).me()).To(Equal(acct.id))

		home := workspace()
		keyPath, publicKey := newSSHKeyPair(home)
		acct.api.addSSHKey("mfa-laptop", publicKey)
		env := append(isolatedSSHEnv(home, filepath.Join(home, "tokens.json")), "NIPA_SSH_KEY="+keyPath, "NIPA_OTP=")
		login := runNipaEnv(home, env, "login", "--host", cli.host)
		Expect(login.ExitCode).To(Equal(0), login.Output())
		Expect(login.Output()).To(ContainSubstring("using SSH key"))
		Expect(login.Output()).NotTo(ContainSubstring("Authentication code"))
	})

	It("prompts for the code on a terminal, retries wrong codes and takes NIPA_OTP on implicit logins", func() {
		acct := newMFAAccount("mfa-prompt")
		acct.enroll()

		home := workspace()
		env := append(isolatedSSHEnv(home, filepath.Join(home, "tokens.json")), "NIPA_OTP=")
		run := startNipaPTY(home, env, "login", "--host", cli.host, "--username", acct.email)
		answerPrompt(run, "Password:", 1, acct.password)
		answerPrompt(run, "Authentication code:", 1, "000000")
		answerPrompt(run, "Invalid code, try again.", 1, acct.nextRecovery())
		Expect(run.wait(30 * time.Second)).To(ContainSubstring("Logged in to " + cli.host + " as " + acct.email))

		failingHome := workspace()
		failing := startNipaPTY(failingHome, append(isolatedSSHEnv(failingHome, filepath.Join(failingHome, "tokens.json")), "NIPA_OTP="),
			"login", "--host", cli.host, "--username", acct.email)
		answerPrompt(failing, "Password:", 1, acct.password)
		for attempt := 1; attempt <= 3; attempt++ {
			answerPrompt(failing, "Authentication code:", attempt, "000000")
		}
		Eventually(failing.done, 30*time.Second).Should(Receive(HaveOccurred()), failing.out.String())
		Expect(strings.ToLower(failing.out.String())).To(ContainSubstring("invalid code"))
		Expect(strings.Count(failing.out.String(), "Authentication code:")).To(Equal(3), "three prompts, then it gives up")
		Expect(fileExists(failingHome, "tokens.json")).To(BeFalse(), "a failed login stores no session")

		project := newProject("mfa-implicit")
		seedRepo(orgSlug, project, map[string][]byte{"readme.txt": []byte("mfa\n")}, "seed")
		api.createPathRule(orgSlug, project, acct.id, "/", 1)

		cloneHome := workspace()
		cloneEnv := append(isolatedSSHEnv(cloneHome, filepath.Join(cloneHome, "tokens.json")), "NIPA_OTP="+acct.nextRecovery())
		clone := startNipaPTY(cloneHome, cloneEnv, "clone", repoURLFor(orgSlug, project), "work")
		answerPrompt(clone, "Username:", 1, acct.email)
		answerPrompt(clone, "Password:", 1, acct.password)
		Expect(clone.wait(60 * time.Second)).NotTo(ContainSubstring("Authentication code:"))
		Expect(readText(filepath.Join(cloneHome, "work"), "readme.txt")).To(Equal("mfa\n"))
	})

	It("finishes a daemon password login in two steps", func() {
		acct := newMFAAccount("mfa-daemon")
		acct.enroll()

		d := startDaemonWithTokens(filepath.Join(GinkgoT().TempDir(), "tokens.json"))
		started, err := d.client.Login(d.ctx, &daemonpb.LoginRequest{Host: cli.host, Username: acct.email, Password: acct.password})
		Expect(err).NotTo(HaveOccurred())
		Expect(started.GetMfaRequired()).To(BeTrue())
		Expect(started.GetMfaChallenge()).NotTo(BeEmpty())
		Expect(started.GetExpiresIn()).To(BeNumerically(">", 0))

		account, err := d.client.AccountStatus(d.ctx, &daemonpb.AccountStatusRequest{Server: cli.host})
		Expect(err).NotTo(HaveOccurred())
		Expect(account.GetSignedIn()).To(BeFalse(), "a pending challenge stores no session")

		_, err = d.client.Login(d.ctx, &daemonpb.LoginRequest{
			Host: cli.host, Username: acct.email, MfaChallenge: started.GetMfaChallenge(), Otp: "000000",
		})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument), "%v", err)

		done, err := d.client.Login(d.ctx, &daemonpb.LoginRequest{
			Host: cli.host, Username: acct.email, MfaChallenge: started.GetMfaChallenge(), Otp: acct.nextRecovery(),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(done.GetMfaRequired()).To(BeFalse())
		account, err = d.client.AccountStatus(d.ctx, &daemonpb.AccountStatusRequest{Server: cli.host})
		Expect(err).NotTo(HaveOccurred())
		Expect(account.GetSignedIn()).To(BeTrue())
		Expect(account.GetEmail()).To(Equal(acct.email))

		oneShot, err := d.client.Login(d.ctx, &daemonpb.LoginRequest{
			Host: cli.host, Username: acct.email, Password: acct.password, Otp: acct.nextRecovery(),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(oneShot.GetMfaRequired()).To(BeFalse(), "a code sent with the password finishes in one call")
	})

	It("is unavailable on a server with MFA_ENABLED=false", func() {
		host := os.Getenv("NIPA_TEST_MFAOFF_HOST")
		if host == "" {
			Skip("needs the MFA_ENABLED=false server from tests/run.sh free")
		}
		baseURL := "http://" + host + "/api/v1"
		admin := newAPIClient(baseURL, cli.user, cli.password)
		address := fmt.Sprintf("mfa-off-%d-%s@example.com", time.Now().UnixNano(), randSuffix(3))
		password := "mfa-off-password-123"
		admin.createUser("mfa off", address, password)
		user := newAPIClient(baseURL, address, password)

		var mfaStatus mfaStatusJSON
		user.do(http.MethodGet, "/me/mfa", nil, &mfaStatus, true)
		Expect(mfaStatus.Available).To(BeFalse())
		Expect(mfaStatus.TOTP.Enabled).To(BeFalse())

		status, body := user.doRaw(http.MethodPost, "/me/mfa/totp", map[string]string{"password": password})
		Expect(status).To(Equal(http.StatusServiceUnavailable), string(body))
		Expect(string(body)).To(ContainSubstring("disabled"))

		status, body = user.doAnonRaw(http.MethodPost, "/auth/login", map[string]string{"email": address, "password": password})
		Expect(status).To(Equal(http.StatusOK), string(body))
		var login mfaLoginJSON
		Expect(json.Unmarshal(body, &login)).To(Succeed())
		Expect(login.MFARequired).To(BeFalse())
		Expect(login.AccessToken).NotTo(BeEmpty())
	})
})

type mfaAccount struct {
	id       string
	email    string
	password string
	api      *apiClient
	codes    *totpCodes
	recovery []string
}

func newMFAAccount(prefix string) *mfaAccount {
	GinkgoHelper()
	address := fmt.Sprintf("%s-%d-%s@example.com", prefix, time.Now().UnixNano(), randSuffix(3))
	password := prefix + "-password-123"
	id := api.createUser(prefix, address, password)
	return &mfaAccount{id: id, email: address, password: password, api: newAPIClient(cli.apiURL, address, password)}
}

// enroll turns TOTP on and keeps the recovery codes; it skips the spec on a
// server that cannot offer two-factor authentication.
func (a *mfaAccount) enroll() {
	GinkgoHelper()
	var mfaStatus mfaStatusJSON
	a.api.do(http.MethodGet, "/me/mfa", nil, &mfaStatus, true)
	if !mfaStatus.Available {
		Skip("two-factor authentication is not available on this server (MFA_ENABLED/MFA_ENCRYPTION_KEY)")
	}
	var enrollment struct {
		Secret string `json:"secret"`
	}
	a.api.do(http.MethodPost, "/me/mfa/totp", map[string]string{"password": a.password}, &enrollment, true)
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret)
	Expect(err).NotTo(HaveOccurred())
	a.codes = &totpCodes{secret: secret}
	var recovery struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	a.api.do(http.MethodPost, "/me/mfa/totp/confirm", map[string]string{"code": a.codes.next()}, &recovery, true)
	Expect(recovery.RecoveryCodes).To(HaveLen(10))
	a.recovery = recovery.RecoveryCodes
}

// nextRecovery spends one recovery code; they work wherever a TOTP code does
// and, unlike TOTP codes, never wait for the next time step.
func (a *mfaAccount) nextRecovery() string {
	GinkgoHelper()
	Expect(a.recovery).NotTo(BeEmpty())
	code := a.recovery[0]
	a.recovery = a.recovery[1:]
	return code
}

// answerPrompt waits for the nth occurrence of prompt and types answer.
func answerPrompt(run *ptyRun, prompt string, nth int, answer string) {
	GinkgoHelper()
	Eventually(func() int {
		return strings.Count(run.out.String(), prompt)
	}, 30*time.Second, 50*time.Millisecond).Should(BeNumerically(">=", nth), func() string { return run.out.String() })
	_, err := run.master.WriteString(answer + "\n")
	Expect(err).NotTo(HaveOccurred())
}
