package suite_test

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type receivedEmail struct {
	From      string
	To        []string
	Subject   string
	Text      string
	HTML      string
	MessageID string
	InReplyTo string
	At        time.Time
}

type sendGridEmailPayload struct {
	Personalizations []struct {
		To []struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"to"`
		Headers map[string]string `json:"headers"`
	} `json:"personalizations"`
	From struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	} `json:"from"`
	Subject string `json:"subject"`
	Content []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"content"`
}

type emailReceiver struct {
	server *httptest.Server

	mu       sync.Mutex
	emails   []receivedEmail
	failures map[string]int
}

// startEmailReceiver binds the SendGrid endpoint nipad was configured with at
// startup, so every notification the server renders lands here.
func startEmailReceiver() *emailReceiver {
	GinkgoHelper()

	port := os.Getenv("NIPA_TEST_EMAIL_PORT")
	Expect(port).NotTo(BeEmpty(), "NIPA_TEST_EMAIL_PORT is not set; run the suite via tests/run.sh")

	receiver := &emailReceiver{failures: map[string]int{}}
	receiver.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var payload sendGridEmailPayload
		Expect(json.Unmarshal(body, &payload)).To(Succeed(), string(body))

		email := receivedEmail{
			From:    payload.From.Email,
			Subject: payload.Subject,
			At:      time.Now(),
		}
		for _, personalization := range payload.Personalizations {
			for _, to := range personalization.To {
				email.To = append(email.To, to.Email)
			}
			email.MessageID = personalization.Headers["Message-ID"]
			email.InReplyTo = personalization.Headers["In-Reply-To"]
		}
		if receiver.consumeFailure(email.To) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		for _, content := range payload.Content {
			switch content.Type {
			case "text/plain":
				email.Text = content.Value
			case "text/html":
				email.HTML = content.Value
			}
		}

		receiver.mu.Lock()
		receiver.emails = append(receiver.emails, email)
		receiver.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	Expect(err).NotTo(HaveOccurred(), "email receiver port %s is not free", port)
	receiver.server.Listener = listener
	receiver.server.Start()
	DeferCleanup(receiver.server.Close)
	return receiver
}

func (r *emailReceiver) all() []receivedEmail {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]receivedEmail(nil), r.emails...)
}

// failFor makes the next count requests to the address fail with a 500, so a
// delivery can exhaust its retries deterministically.
func (r *emailReceiver) failFor(address string, count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures[address] = count
}

func (r *emailReceiver) consumeFailure(recipients []string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, recipient := range recipients {
		if remaining := r.failures[recipient]; remaining > 0 {
			r.failures[recipient] = remaining - 1
			return true
		}
	}
	return false
}

func (r *emailReceiver) countFor(address string) int {
	count := 0
	for _, email := range r.all() {
		for _, to := range email.To {
			if to == address {
				count++
			}
		}
	}
	return count
}

func (r *emailReceiver) waitFor(address string) receivedEmail {
	GinkgoHelper()

	var found receivedEmail
	Eventually(func() bool {
		for _, email := range r.all() {
			for _, to := range email.To {
				if to == address {
					found = email
					return true
				}
			}
		}
		return false
	}, "15s", "50ms").Should(BeTrue(), "no email for %s; got %v", address, r.recipients())
	return found
}

func (r *emailReceiver) waitForSubject(address, subject string) receivedEmail {
	GinkgoHelper()

	var found receivedEmail
	Eventually(func() bool {
		for _, email := range r.all() {
			if !contains(email.To, address) || !strings.Contains(email.Subject, subject) {
				continue
			}
			found = email
			return true
		}
		return false
	}, "15s", "50ms").Should(BeTrue(), "no %q email for %s; got %v", subject, address, r.subjects())
	return found
}

func (r *emailReceiver) recipients() []string {
	recipients := []string{}
	for _, email := range r.all() {
		recipients = append(recipients, email.To...)
	}
	return recipients
}

func (r *emailReceiver) subjects() []string {
	subjects := []string{}
	for _, email := range r.all() {
		subjects = append(subjects, email.Subject)
	}
	return subjects
}

func contains(values []string, value string) bool {
	for _, entry := range values {
		if entry == value {
			return true
		}
	}
	return false
}
