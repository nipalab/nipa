package suite_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/gomega"
)

type apiClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newAPIClient(baseURL, email, password string) *apiClient {
	c := &apiClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
	c.token = c.login(email, password)
	return c
}

func (c *apiClient) login(email, password string) string {
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	c.do(http.MethodPost, "/auth/login", map[string]string{
		"email":    email,
		"password": password,
	}, &resp, false)
	Expect(resp.AccessToken).NotTo(BeEmpty(), "login returned no access token")
	return resp.AccessToken
}

func (c *apiClient) createOrg(name, slug string) {
	c.do(http.MethodPost, "/orgs", map[string]string{
		"name": name,
		"slug": slug,
	}, nil, true)
}

func (c *apiClient) createProject(org, name, slug string) {
	c.do(http.MethodPost, "/orgs/"+org+"/projects", map[string]string{
		"name": name,
		"slug": slug,
	}, nil, true)
}

func (c *apiClient) createUser(name, email, password string) string {
	var resp struct {
		ID string `json:"id"`
	}
	c.do(http.MethodPost, "/users", map[string]string{
		"name":     name,
		"email":    email,
		"password": password,
	}, &resp, true)
	Expect(resp.ID).NotTo(BeEmpty(), "user creation returned no id")
	return resp.ID
}

// createPathRule grants a user a path prefix permission; permission is the
// PBAC bitmask (1 read, 2 write, 4 lock).
func (c *apiClient) createPathRule(org, project, userID, pathPrefix string, permission uint64) {
	c.do(http.MethodPost, fmt.Sprintf("/orgs/%s/projects/%s/permissions/rules", org, project), map[string]any{
		"user_id":     userID,
		"path_prefix": pathPrefix,
		"permission":  permission,
	}, nil, true)
}

func (c *apiClient) setAdmin(userID string) {
	c.do(http.MethodPatch, "/users/"+userID+"/admin", map[string]bool{"is_admin": true}, nil, true)
}

type webhookJSON struct {
	ID          string   `json:"id"`
	ProjectID   string   `json:"project_id"`
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	Events      []string `json:"events"`
	PathPrefix  string   `json:"path_prefix"`
	IsActive    bool     `json:"is_active"`
	InsecureTLS bool     `json:"insecure_tls"`
	Secret      string   `json:"secret"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

type webhookDeliveryJSON struct {
	ID             string  `json:"id"`
	WebhookID      string  `json:"webhook_id"`
	EventType      string  `json:"event_type"`
	State          string  `json:"state"`
	Attempt        int64   `json:"attempt"`
	ResponseStatus *int64  `json:"response_status"`
	LastError      string  `json:"last_error"`
	DeliveredAt    *string `json:"delivered_at"`
}

func (c *apiClient) createWebhook(org, project string, body map[string]any) webhookJSON {
	var out webhookJSON
	c.do(http.MethodPost, "/orgs/"+org+"/projects/"+project+"/webhooks", body, &out, true)
	return out
}

func (c *apiClient) updateWebhook(org, project, id string, patch map[string]any) webhookJSON {
	var out webhookJSON
	c.do(http.MethodPatch, "/orgs/"+org+"/projects/"+project+"/webhooks/"+id, patch, &out, true)
	return out
}

func (c *apiClient) rotateWebhookSecret(org, project, id string) webhookJSON {
	var out webhookJSON
	c.do(http.MethodPost, "/orgs/"+org+"/projects/"+project+"/webhooks/"+id+"/rotate-secret", nil, &out, true)
	return out
}

func (c *apiClient) testWebhook(org, project, id string) webhookDeliveryJSON {
	var out webhookDeliveryJSON
	c.do(http.MethodPost, "/orgs/"+org+"/projects/"+project+"/webhooks/"+id+"/test", nil, &out, true)
	return out
}

func (c *apiClient) listWebhookDeliveries(org, project, id string) []webhookDeliveryJSON {
	var out []webhookDeliveryJSON
	c.do(http.MethodGet, "/orgs/"+org+"/projects/"+project+"/webhooks/"+id+"/deliveries?limit=50", nil, &out, true)
	return out
}

func (c *apiClient) redeliverWebhook(org, project, id, deliveryID string) webhookDeliveryJSON {
	var out webhookDeliveryJSON
	c.do(http.MethodPost, "/orgs/"+org+"/projects/"+project+"/webhooks/"+id+"/deliveries/"+deliveryID+"/redeliver", nil, &out, true)
	return out
}

func (c *apiClient) reopenMergeRequest(org, project string, number int64) {
	c.do(http.MethodPost, fmt.Sprintf("/orgs/%s/projects/%s/merge-requests/%d/reopen", org, project, number), nil, nil, true)
}

type branchProtectionJSON struct {
	IsProtected           bool  `json:"is_protected"`
	RequiredApprovals     int64 `json:"required_approvals"`
	DismissStaleApprovals bool  `json:"dismiss_stale_approvals"`
}

func (c *apiClient) setBranchProtection(org, project, branch string, protected bool, requiredApprovals *int64, dismissStaleApprovals *bool) branchProtectionJSON {
	body := map[string]any{"protected": protected}
	if requiredApprovals != nil {
		body["required_approvals"] = *requiredApprovals
	}
	if dismissStaleApprovals != nil {
		body["dismiss_stale_approvals"] = *dismissStaleApprovals
	}
	var out branchProtectionJSON
	c.do(http.MethodPut, fmt.Sprintf("/orgs/%s/projects/%s/branches/%s/protection", org, project, branch), body, &out, true)
	return out
}

type mergeabilityJSON struct {
	Status    string `json:"status"`
	BlockedBy string `json:"blocked_by"`
}

func (c *apiClient) checkMergeRequest(org, project string, number int64) mergeabilityJSON {
	var out mergeabilityJSON
	c.do(http.MethodGet, fmt.Sprintf("/orgs/%s/projects/%s/merge-requests/%d/check", org, project, number), nil, &out, true)
	return out
}

type mergedRequestJSON struct {
	Number int64  `json:"number"`
	Status string `json:"status"`
}

// mergeMergeRequest lands a request over the REST API with an explicit
// strategy and delete-source option.
func (c *apiClient) mergeMergeRequest(org, project string, number int64, strategy string, deleteSource bool) mergedRequestJSON {
	body := map[string]any{}
	if strategy != "" {
		body["strategy"] = strategy
	}
	if deleteSource {
		body["delete_source"] = true
	}
	var out mergedRequestJSON
	c.do(http.MethodPost, fmt.Sprintf("/orgs/%s/projects/%s/merge-requests/%d/merge", org, project, number), body, &out, true)
	return out
}

func (c *apiClient) submitMergeRequestReview(org, project string, number int64, state, body string) string {
	var out struct {
		ID string `json:"id"`
	}
	c.do(http.MethodPost, fmt.Sprintf("/orgs/%s/projects/%s/merge-requests/%d/reviews", org, project, number),
		map[string]string{"state": state, "body": body}, &out, true)
	return out.ID
}

// submitMergeRequestReviewWithComment posts a review decision carrying one
// inline comment and returns the review id.
func (c *apiClient) submitMergeRequestReviewWithComment(org, project string, number int64, state, body, filePath string, newLine int64) string {
	var out struct {
		ID string `json:"id"`
	}
	c.do(http.MethodPost, fmt.Sprintf("/orgs/%s/projects/%s/merge-requests/%d/reviews", org, project, number),
		map[string]any{
			"state": state,
			"body":  body,
			"comments": []map[string]any{
				{"file_path": filePath, "new_line": newLine, "body": "inline review note"},
			},
		}, &out, true)
	return out.ID
}

func (c *apiClient) dismissMergeRequestReview(org, project string, number int64, reviewID string) {
	c.do(http.MethodPost,
		fmt.Sprintf("/orgs/%s/projects/%s/merge-requests/%d/reviews/%s/dismiss", org, project, number, reviewID),
		map[string]string{}, nil, true)
}

func (c *apiClient) withdrawMergeRequestReview(org, project string, number int64, reviewID string) {
	c.do(http.MethodDelete,
		fmt.Sprintf("/orgs/%s/projects/%s/merge-requests/%d/reviews/%s", org, project, number, reviewID),
		nil, nil, true)
}

func (c *apiClient) updateMergeRequestComment(org, project string, number int64, threadID, commentID, body string) {
	c.do(http.MethodPatch,
		fmt.Sprintf("/orgs/%s/projects/%s/merge-requests/%d/threads/%s/comments/%s", org, project, number, threadID, commentID),
		map[string]string{"body": body}, nil, true)
}

func (c *apiClient) deleteMergeRequestComment(org, project string, number int64, threadID, commentID string) {
	c.do(http.MethodDelete,
		fmt.Sprintf("/orgs/%s/projects/%s/merge-requests/%d/threads/%s/comments/%s", org, project, number, threadID, commentID),
		nil, nil, true)
}

func (c *apiClient) deleteMergeRequestThread(org, project string, number int64, threadID string) {
	c.do(http.MethodDelete,
		fmt.Sprintf("/orgs/%s/projects/%s/merge-requests/%d/threads/%s", org, project, number, threadID),
		nil, nil, true)
}

func (c *apiClient) addMergeRequestComment(org, project string, number int64, filePath, body string) {
	c.do(http.MethodPost, fmt.Sprintf("/orgs/%s/projects/%s/merge-requests/%d/threads", org, project, number),
		map[string]string{"file_path": filePath, "body": body}, nil, true)
}

func (c *apiClient) do(method, path string, body any, out any, auth bool) int {
	var reader io.Reader
	hasBody := body != nil
	if body != nil {
		data, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		reader = bytes.NewReader(data)
	} else if method == http.MethodPost || method == http.MethodPatch || method == http.MethodPut {
		// The REST services consume JSON, so body-less mutations still need an
		// application/json payload.
		reader = bytes.NewReader([]byte("{}"))
		hasBody = true
	}

	req, err := http.NewRequest(method, c.baseURL+path, reader)
	Expect(err).NotTo(HaveOccurred())
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(BeNumerically(">=", 200), "%s %s: %s", method, path, data)
	Expect(resp.StatusCode).To(BeNumerically("<", 300), "%s %s: %s", method, path, data)

	if out != nil {
		Expect(json.Unmarshal(data, out)).To(Succeed())
	}
	return resp.StatusCode
}
