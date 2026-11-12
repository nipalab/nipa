package suite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/metadata"

	clientdomain "github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
)

var _ = Describe("rate limiting", func() {
	BeforeEach(func() {
		if os.Getenv("NIPA_TEST_RATELIMIT") != "1" {
			Skip("rate limit specs need the low auth_id limits from tests/run.sh")
		}
	})

	It("throttles password logins per identity over REST and gRPC", func() {
		addr := fmt.Sprintf("ratelimit-%d@example.com", time.Now().UnixNano())
		body := map[string]string{"email": addr, "password": "wrong-password"}

		limited := false
		for attempt := 0; attempt < 120 && !limited; attempt++ {
			status, raw := api.doAnonRaw(http.MethodPost, "/auth/login", body)
			if status == http.StatusTooManyRequests {
				limited = true
				Expect(string(raw)).To(ContainSubstring("rate limit exceeded"))
				break
			}
			Expect(status).To(BeNumerically("<", 500), string(raw))
		}
		Expect(limited).To(BeTrue(), "expected a 429 before 120 attempts")

		transport := clientgrpc.NewTransport()
		Expect(transport.Connect(cli.host)).To(Succeed())
		_, err := transport.LoginWithUsernamePassword(context.Background(), cli.host, addr, "wrong-password")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("rate limit exceeded"))
	})

	It("keeps other identities unaffected", func() {
		status, _ := api.doAnonRaw(http.MethodPost, "/auth/login", map[string]string{
			"email": fmt.Sprintf("ratelimit-other-%d@example.com", time.Now().UnixNano()), "password": "nope",
		})
		Expect(status).NotTo(Equal(http.StatusTooManyRequests))
	})

	Context("against the low-limit server", func() {
		const (
			authIPBurst    = 4
			authIDBurst    = 3
			authEmailBurst = 6
			apiUserBurst   = 6
			chunkIPBurst   = 3
			globalIPBurst  = 10
		)

		var rl *rlServer

		BeforeEach(func() {
			host := os.Getenv("NIPA_TEST_RATELIMIT_HOST")
			if host == "" {
				Skip("needs the low-limit server from NIPA_TEST_RATELIMIT=1 tests/run.sh free")
			}
			rl = newRLServer(host, "")
		})

		It("throttles the auth routes per client IP over REST and gRPC", func() {
			ip := rlFreshIP()
			login := func(email string) rlResponse {
				return rl.send(http.MethodPost, "/api/v1/auth/login", ip, "", map[string]string{
					"email": email, "password": "wrong-password",
				})
			}
			for i := 0; i < authIPBurst; i++ {
				res := login(rlEmail("auth-ip"))
				Expect(res.status).NotTo(Equal(http.StatusTooManyRequests), "attempt %d: %s", i, res.body)
				Expect(res.status).To(BeNumerically("<", 500), res.body)
			}
			res := login(rlEmail("auth-ip"))
			expectLimited(res, authIPBurst)

			ctx := metadata.AppendToOutgoingContext(context.Background(), "x-forwarded-for", ip)
			transport := clientgrpc.NewTransport()
			Expect(transport.Connect(rl.host)).To(Succeed())
			_, err := transport.LoginWithUsernamePassword(ctx, rl.host, rlEmail("auth-ip-grpc"), "wrong-password")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("rate limit exceeded"))
			var derr *clientdomain.Error
			Expect(errors.As(err, &derr)).To(BeTrue(), "%T: %v", err, err)
			Expect(derr.Code).To(Equal(http.StatusTooManyRequests))

			other := metadata.AppendToOutgoingContext(context.Background(), "x-forwarded-for", rlFreshIP())
			_, err = transport.LoginWithUsernamePassword(other, rl.host, rlEmail("auth-ip-grpc"), "wrong-password")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("rate limit exceeded"))
		})

		It("keys login throttling on email and client IP under an email-wide ceiling", func() {
			admin := rl.login(cli.user, cli.password)
			email, password := rlEmail("auth-id"), "ratelimit-password-123"
			rl.createUser(admin, email, password)
			login := func(ip, pw string) rlResponse {
				return rl.send(http.MethodPost, "/api/v1/auth/login", ip, "", map[string]string{"email": email, "password": pw})
			}
			expectThrottled := func(res rlResponse) {
				GinkgoHelper()
				Expect(res.err).NotTo(HaveOccurred())
				Expect(res.status).To(Equal(http.StatusTooManyRequests), res.body)
				Expect(res.body).To(ContainSubstring("rate limit exceeded"))
			}

			attacker := rlFreshIP()
			for i := 0; i < authIDBurst; i++ {
				res := login(attacker, "wrong-password")
				Expect(res.status).To(Equal(http.StatusBadRequest), "attempt %d: %s", i, res.body)
			}
			expectThrottled(login(attacker, "wrong-password"))

			Expect(rl.login(email, password)).NotTo(BeEmpty(), "another client IP still signs in")

			used := authIDBurst + 1
			for ; used < authEmailBurst; used++ {
				res := login(rlFreshIP(), "wrong-password")
				Expect(res.status).To(Equal(http.StatusBadRequest), "attempt %d: %s", used, res.body)
			}
			expectThrottled(login(rlFreshIP(), password))
		})

		It("throttles authenticated REST calls per user", func() {
			admin := rl.login(cli.user, cli.password)
			password := "ratelimit-password-123"
			first, second := rlEmail("api-user-a"), rlEmail("api-user-b")
			rl.createUser(admin, first, password)
			rl.createUser(admin, second, password)
			tokenA := rl.login(first, password)
			tokenB := rl.login(second, password)

			for i := 0; i < apiUserBurst; i++ {
				res := rl.send(http.MethodGet, "/api/v1/orgs", rlFreshIP(), tokenA, nil)
				Expect(res.status).To(Equal(http.StatusOK), "call %d: %s", i, res.body)
				Expect(res.header.Get("X-RateLimit-Limit")).To(Equal(strconv.Itoa(apiUserBurst)))
			}
			expectLimited(rl.send(http.MethodGet, "/api/v1/orgs", rlFreshIP(), tokenA, nil), apiUserBurst)

			res := rl.send(http.MethodGet, "/api/v1/orgs", rlFreshIP(), tokenB, nil)
			Expect(res.status).To(Equal(http.StatusOK), res.body)
		})

		It("throttles the signed chunk routes per client IP", func() {
			ip := rlFreshIP()
			path := "/api/chunks/org/project/" + randSuffix(32) + "?op=get&size=1&exp=1&sig=00"
			for i := 0; i < chunkIPBurst; i++ {
				res := rl.send(http.MethodGet, path, ip, "", nil)
				Expect(res.status).NotTo(Equal(http.StatusTooManyRequests), "attempt %d: %s", i, res.body)
			}
			expectLimited(rl.send(http.MethodGet, path, ip, "", nil), chunkIPBurst)

			res := rl.send(http.MethodGet, "/", ip, "", nil)
			Expect(res.status).To(Equal(http.StatusOK), "chunk traffic must not drain the global bucket")
		})

		It("throttles every other route per client IP and counts the decisions", func() {
			limitedBefore := rl.counter("global_ip", "limited")
			allowedBefore := rl.counter("global_ip", "allowed")

			ip := rlFreshIP()
			for i := 0; i < globalIPBurst; i++ {
				res := rl.send(http.MethodGet, "/", ip, "", nil)
				Expect(res.status).To(Equal(http.StatusOK), "attempt %d: %s", i, res.body)
			}
			expectLimited(rl.send(http.MethodGet, "/", ip, "", nil), globalIPBurst)
			expectLimited(rl.send(http.MethodGet, "/api/v1/orgs", ip, "", nil), globalIPBurst)

			Expect(rl.send(http.MethodGet, "/metrics", ip, "", nil).status).To(Equal(http.StatusOK))
			Expect(rl.counter("global_ip", "limited")).To(BeNumerically(">=", limitedBefore+2))
			Expect(rl.counter("global_ip", "allowed")).To(BeNumerically(">=", allowedBefore+globalIPBurst))
		})

		It("honours X-Forwarded-For from a trusted proxy by its right-most untrusted hop", func() {
			ip := rlFreshIP()
			for i := 0; i < globalIPBurst; i++ {
				Expect(rl.send(http.MethodGet, "/", ip, "", nil).status).To(Equal(http.StatusOK))
			}
			expectLimited(rl.send(http.MethodGet, "/", ip, "", nil), globalIPBurst)
			expectLimited(rl.send(http.MethodGet, "/", rlFreshIP()+", "+ip, "", nil), globalIPBurst)
			expectLimited(rl.send(http.MethodGet, "/", ip+", 127.0.0.1", "", nil), globalIPBurst)

			Expect(rl.send(http.MethodGet, "/", rlFreshIP(), "", nil).status).To(Equal(http.StatusOK))
			Expect(rl.send(http.MethodGet, "/", ip+", "+rlFreshIP(), "", nil).status).To(Equal(http.StatusOK))
		})

		It("ignores X-Forwarded-For from an untrusted peer", func() {
			untrusted := newRLServer(rl.host, "127.0.0.2")
			first := untrusted.send(http.MethodGet, "/", rlFreshIP(), "", nil)
			if first.err != nil {
				Skip("cannot dial from 127.0.0.2: " + first.err.Error())
			}
			Expect(first.status).To(Equal(http.StatusOK))
			for i := 1; i < globalIPBurst; i++ {
				res := untrusted.send(http.MethodGet, "/", rlFreshIP(), "", nil)
				Expect(res.err).NotTo(HaveOccurred())
				Expect(res.status).To(Equal(http.StatusOK), "attempt %d", i)
			}
			expectLimited(untrusted.send(http.MethodGet, "/", rlFreshIP(), "", nil), globalIPBurst)

			Expect(rl.send(http.MethodGet, "/", rlFreshIP(), "", nil).status).To(Equal(http.StatusOK))
		})
	})
})

type rlServer struct {
	host string
	http *http.Client
}

type rlResponse struct {
	status int
	header http.Header
	body   string
	err    error
}

func newRLServer(host, sourceIP string) *rlServer {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if sourceIP != "" {
		dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(sourceIP)}
	}
	transport := &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true}
	return &rlServer{host: host, http: &http.Client{Timeout: 30 * time.Second, Transport: transport}}
}

func (s *rlServer) send(method, path, xff, token string, body any) rlResponse {
	GinkgoHelper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, "http://"+s.host+path, reader)
	Expect(err).NotTo(HaveOccurred())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return rlResponse{err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return rlResponse{status: resp.StatusCode, header: resp.Header, body: string(data)}
}

func (s *rlServer) login(email, password string) string {
	GinkgoHelper()
	res := s.send(http.MethodPost, "/api/v1/auth/login", rlFreshIP(), "", map[string]string{
		"email": email, "password": password,
	})
	Expect(res.err).NotTo(HaveOccurred())
	Expect(res.status).To(Equal(http.StatusOK), res.body)
	var out struct {
		AccessToken string `json:"access_token"`
	}
	Expect(json.Unmarshal([]byte(res.body), &out)).To(Succeed())
	Expect(out.AccessToken).NotTo(BeEmpty())
	return out.AccessToken
}

func (s *rlServer) createUser(token, email, password string) {
	GinkgoHelper()
	res := s.send(http.MethodPost, "/api/v1/users", rlFreshIP(), token, map[string]string{
		"name": "rate limit user", "email": email, "password": password,
	})
	Expect(res.err).NotTo(HaveOccurred())
	Expect(res.status).To(BeNumerically(">=", 200), res.body)
	Expect(res.status).To(BeNumerically("<", 300), res.body)
}

func (s *rlServer) counter(scope, result string) float64 {
	GinkgoHelper()
	res := s.send(http.MethodGet, "/metrics", "", "", nil)
	Expect(res.err).NotTo(HaveOccurred())
	Expect(res.status).To(Equal(http.StatusOK), res.body)
	pattern := regexp.MustCompile(`(?m)^nipa_ratelimit_total\{result="` + result + `",scope="` + scope + `"\} (\S+)$`)
	m := pattern.FindStringSubmatch(res.body)
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	Expect(err).NotTo(HaveOccurred())
	return v
}

func expectLimited(res rlResponse, burst int) {
	GinkgoHelper()
	Expect(res.err).NotTo(HaveOccurred())
	Expect(res.status).To(Equal(http.StatusTooManyRequests), res.body)
	Expect(res.body).To(ContainSubstring("rate limit exceeded"))
	Expect(res.header.Get("X-RateLimit-Limit")).To(Equal(strconv.Itoa(burst)))
	retry, err := strconv.Atoi(res.header.Get("Retry-After"))
	Expect(err).NotTo(HaveOccurred(), "Retry-After %q", res.header.Get("Retry-After"))
	Expect(retry).To(BeNumerically(">=", 1))
}

var rlIPSeq = atomic.Uint32{}

func init() { rlIPSeq.Store(rand.Uint32N(60000)) }

// rlFreshIP hands out addresses from the 198.18.0.0/15 benchmark range so
// every bucket a spec drains is its own.
func rlFreshIP() string {
	n := rlIPSeq.Add(1) % 65000
	return fmt.Sprintf("198.18.%d.%d", n/250, n%250+1)
}

func rlEmail(prefix string) string {
	return fmt.Sprintf("rl-%s-%d-%s@example.com", prefix, time.Now().UnixNano(), randSuffix(3))
}
