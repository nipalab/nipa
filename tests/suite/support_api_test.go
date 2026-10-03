package suite_test

import (
	"bytes"
	"encoding/json"
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

func (c *apiClient) setAdmin(userID string) {
	c.do(http.MethodPatch, "/users/"+userID+"/admin", map[string]bool{"is_admin": true}, nil, true)
}

func (c *apiClient) do(method, path string, body any, out any, auth bool) int {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reader)
	Expect(err).NotTo(HaveOccurred())
	if body != nil {
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
