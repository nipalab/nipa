package suite_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nipalab/nipa/internal/webhook"
)

type receivedDelivery struct {
	Event     string
	Delivery  string
	HookID    string
	Signature string
	Content   string
	Agent     string
	Body      []byte
	At        time.Time
}

type hookEnvelope struct {
	Event     string    `json:"event"`
	Timestamp time.Time `json:"timestamp"`
	Actor     struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Email    string `json:"email"`
	} `json:"actor"`
	Organization struct {
		Slug string `json:"slug"`
	} `json:"organization"`
	Project struct {
		Slug          string `json:"slug"`
		DefaultBranch string `json:"default_branch"`
	} `json:"project"`
	WebhookID string `json:"webhook_id"`
}

type hookPushPayload struct {
	hookEnvelope
	Changes []hookPushChange `json:"changes"`
}

type hookPushChange struct {
	Branch         string         `json:"branch"`
	BranchID       string         `json:"branch_id"`
	Created        bool           `json:"created"`
	Deleted        bool           `json:"deleted"`
	Forced         bool           `json:"forced"`
	Before         string         `json:"before"`
	After          string         `json:"after"`
	Message        string         `json:"message"`
	Files          []hookPushFile `json:"files"`
	FilesTruncated bool           `json:"files_truncated"`
}

type hookPushFile struct {
	Path      string `json:"path"`
	Operation string `json:"op"`
	Binary    bool   `json:"binary"`
	SizeBytes int64  `json:"size_bytes"`
}

type hookMRPayload struct {
	hookEnvelope
	MergeRequest struct {
		ID            string    `json:"id"`
		Number        int64     `json:"number"`
		Title         string    `json:"title"`
		Description   string    `json:"description"`
		State         string    `json:"state"`
		SourceBranch  string    `json:"source_branch"`
		TargetBranch  string    `json:"target_branch"`
		AuthorID      string    `json:"author_id"`
		MergeCommitID string    `json:"merge_commit_id"`
		CreatedAt     time.Time `json:"created_at"`
		UpdatedAt     time.Time `json:"updated_at"`
	} `json:"merge_request"`
}

type hookBranchPayload struct {
	hookEnvelope
	Branch struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		CommitID string `json:"commit_id"`
		Default  bool   `json:"default"`
	} `json:"branch"`
}

type hookTagPayload struct {
	hookEnvelope
	Tag struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		CommitID  string `json:"commit_id"`
		Message   string `json:"message"`
		CreatedBy string `json:"created_by"`
	} `json:"tag"`
}

type hookReceiver struct {
	server *httptest.Server

	mu         sync.Mutex
	deliveries []receivedDelivery
	status     int
}

func startHookReceiver() *hookReceiver {
	GinkgoHelper()

	r := &hookReceiver{status: http.StatusOK}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		r.mu.Lock()
		r.deliveries = append(r.deliveries, receivedDelivery{
			Event:     req.Header.Get(webhook.EventHeader),
			Delivery:  req.Header.Get(webhook.DeliveryHeader),
			HookID:    req.Header.Get(webhook.HookHeader),
			Signature: req.Header.Get(webhook.SignatureHeader),
			Content:   req.Header.Get("Content-Type"),
			Agent:     req.Header.Get("User-Agent"),
			Body:      body,
			At:        time.Now(),
		})
		status := r.status
		r.mu.Unlock()
		w.WriteHeader(status)
	}))
	DeferCleanup(r.server.Close)
	return r
}

func (r *hookReceiver) url(path string) string {
	return r.server.URL + path
}

func (r *hookReceiver) setResponseCode(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = status
}

func (r *hookReceiver) all() []receivedDelivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]receivedDelivery(nil), r.deliveries...)
}

func (r *hookReceiver) events() []string {
	events := []string{}
	for _, delivery := range r.all() {
		events = append(events, delivery.Event)
	}
	return events
}

func (r *hookReceiver) count(event string) int {
	count := 0
	for _, delivery := range r.all() {
		if delivery.Event == event {
			count++
		}
	}
	return count
}

func (r *hookReceiver) wait(event string) receivedDelivery {
	GinkgoHelper()

	var found receivedDelivery
	Eventually(func() bool {
		for _, delivery := range r.all() {
			if delivery.Event == event {
				found = delivery
				return true
			}
		}
		return false
	}, "10s", "50ms").Should(BeTrue(), "no %q delivery received; got %v", event, r.events())
	return found
}

func (r *hookReceiver) waitCount(event string, count int) receivedDelivery {
	GinkgoHelper()

	Eventually(func() int {
		return r.count(event)
	}, "10s", "50ms").Should(BeNumerically(">=", count), "got events %v", r.events())
	deliveries := r.all()
	seen := 0
	for _, delivery := range deliveries {
		if delivery.Event == event {
			seen++
			if seen == count {
				return delivery
			}
		}
	}
	Fail("delivery not found")
	return receivedDelivery{}
}

func (d receivedDelivery) decode(out any) {
	GinkgoHelper()
	Expect(json.Unmarshal(d.Body, out)).To(Succeed(), string(d.Body))
}

func (d receivedDelivery) pushPayload() hookPushPayload {
	GinkgoHelper()
	var payload hookPushPayload
	d.decode(&payload)
	return payload
}

func (d receivedDelivery) mrPayload() hookMRPayload {
	GinkgoHelper()
	var payload hookMRPayload
	d.decode(&payload)
	return payload
}

func (d receivedDelivery) branchPayload() hookBranchPayload {
	GinkgoHelper()
	var payload hookBranchPayload
	d.decode(&payload)
	return payload
}

func (d receivedDelivery) tagPayload() hookTagPayload {
	GinkgoHelper()
	var payload hookTagPayload
	d.decode(&payload)
	return payload
}

func (d receivedDelivery) envelope() hookEnvelope {
	GinkgoHelper()
	var envelope hookEnvelope
	d.decode(&envelope)
	return envelope
}

func (d receivedDelivery) verifySignature(secret string) bool {
	return webhook.Verify(secret, d.Body, d.Signature)
}
