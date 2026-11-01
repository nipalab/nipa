package suite_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
})
