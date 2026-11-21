// Package jiratest is an in-memory Jira speaking the /rest/api/2 subset the
// jira client uses, for unit tests and the e2e harnesses.
package jiratest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/nipalab/nipa/internal/issuetracker"
)

type Issue struct {
	Key      string
	Summary  string
	Type     string
	Status   string
	Assignee string
}

type RemoteLink struct {
	GlobalID string
	URL      string
	Title    string
	Summary  string
	Resolved bool
}

type Transition struct {
	ID string
	To string
}

type Options struct {
	// AuthKind is issuetracker.AuthBasic (default) or AuthBearer.
	AuthKind   string
	Account    string
	Secret     string
	Deployment string
}

// Failure is answered instead of the next matching request.
type Failure struct {
	Status     int
	RetryAfter string
	Body       string
}

type Server struct {
	*httptest.Server

	opts Options

	mu          sync.Mutex
	issues      map[string]*Issue
	links       map[string][]RemoteLink
	comments    map[string][]string
	statuses    map[string]string
	workflow    map[string][]Transition
	failures    []Failure
	requests    []string
	lastHeaders http.Header
}

// DefaultStatuses maps the default workflow's statuses to their category key.
var DefaultStatuses = map[string]string{
	"To Do":       "new",
	"In Progress": "indeterminate",
	"In Review":   "indeterminate",
	"Done":        "done",
}

func New(opts Options) *Server {
	if opts.AuthKind == "" {
		opts.AuthKind = issuetracker.AuthBasic
	}
	if opts.Account == "" {
		opts.Account = "bot@example.com"
	}
	if opts.Secret == "" {
		opts.Secret = "jira-test-token"
	}
	if opts.Deployment == "" {
		opts.Deployment = "Cloud"
	}
	s := &Server{
		opts:     opts,
		issues:   map[string]*Issue{},
		links:    map[string][]RemoteLink{},
		comments: map[string][]string{},
		statuses: map[string]string{},
		workflow: map[string][]Transition{},
	}
	for name, category := range DefaultStatuses {
		s.statuses[name] = category
	}
	s.workflow["To Do"] = []Transition{{"11", "In Progress"}, {"31", "Done"}}
	s.workflow["In Progress"] = []Transition{{"21", "In Review"}, {"31", "Done"}, {"41", "To Do"}}
	s.workflow["In Review"] = []Transition{{"31", "Done"}, {"11", "In Progress"}}
	s.workflow["Done"] = []Transition{{"51", "To Do"}}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /rest/api/2/myself", s.myself)
	mux.HandleFunc("GET /rest/api/2/serverInfo", s.serverInfo)
	mux.HandleFunc("GET /rest/api/2/issue/picker", s.picker)
	mux.HandleFunc("GET /rest/api/2/issue/{key}", s.issue)
	mux.HandleFunc("POST /rest/api/2/issue/{key}/remotelink", s.remoteLink)
	mux.HandleFunc("DELETE /rest/api/2/issue/{key}/remotelink", s.deleteRemoteLink)
	mux.HandleFunc("POST /rest/api/2/issue/{key}/comment", s.comment)
	mux.HandleFunc("GET /rest/api/2/issue/{key}/transitions", s.transitions)
	mux.HandleFunc("POST /rest/api/2/issue/{key}/transitions", s.transition)
	s.Server = httptest.NewServer(s.middleware(mux))
	return s
}

// Config returns a tracker config that signs in to this server.
func (s *Server) Config() issuetracker.Config {
	cfg := issuetracker.Config{BaseURL: s.URL, Secret: s.opts.Secret, AuthKind: s.opts.AuthKind, Transport: http.DefaultTransport}
	if s.opts.AuthKind == issuetracker.AuthBasic {
		cfg.Account = s.opts.Account
	}
	return cfg
}

func (s *Server) AddIssue(issue Issue) {
	if issue.Status == "" {
		issue.Status = "To Do"
	}
	if issue.Type == "" {
		issue.Type = "Task"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issues[issue.Key] = &issue
}

func (s *Server) Issue(key string) (Issue, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	issue, ok := s.issues[key]
	if !ok {
		return Issue{}, false
	}
	return *issue, true
}

func (s *Server) RemoteLinks(key string) []RemoteLink {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.links[key])
}

func (s *Server) Comments(key string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.comments[key])
}

// Requests lists "METHOD path" of every request received.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

func (s *Server) LastHeaders() http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastHeaders.Clone()
}

// Fail queues failures answered, in order, instead of the next requests.
func (s *Server) Fail(failures ...Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, failures...)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		s.lastHeaders = r.Header.Clone()
		var failure *Failure
		if len(s.failures) > 0 {
			failure = &s.failures[0]
			s.failures = s.failures[1:]
		}
		s.mu.Unlock()
		if failure != nil {
			if failure.RetryAfter != "" {
				w.Header().Set("Retry-After", failure.RetryAfter)
			}
			w.WriteHeader(failure.Status)
			_, _ = w.Write([]byte(failure.Body))
			return
		}
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, "Client must be authenticated to access this resource.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	if s.opts.AuthKind == issuetracker.AuthBearer {
		return header == "Bearer "+s.opts.Secret
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(s.opts.Account+":"+s.opts.Secret))
	return header == want
}

func (s *Server) myself(w http.ResponseWriter, _ *http.Request) {
	me := map[string]string{"displayName": "Nipa Bot", "emailAddress": s.opts.Account}
	if s.opts.Deployment == "Cloud" {
		me["accountId"] = "5b10a2844c20165700ede21g"
	} else {
		me["name"] = "nipa-bot"
		me["key"] = "JIRAUSER10000"
	}
	writeJSON(w, http.StatusOK, me)
}

func (s *Server) serverInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"baseUrl":        s.URL,
		"version":        "1001.0.0-SNAPSHOT",
		"deploymentType": s.opts.Deployment,
		"serverTitle":    "Fake Jira",
	})
}

func (s *Server) lookup(w http.ResponseWriter, r *http.Request) (*Issue, bool) {
	issue, ok := s.issues[r.PathValue("key")]
	if !ok {
		writeError(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
	}
	return issue, ok
}

func (s *Server) issue(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	issue, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var assignee any
	if issue.Assignee != "" {
		assignee = map[string]string{"displayName": issue.Assignee}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":  "10001",
		"key": issue.Key,
		"fields": map[string]any{
			"summary":   issue.Summary,
			"issuetype": map[string]string{"name": issue.Type},
			"assignee":  assignee,
			"status": map[string]any{
				"name":           issue.Status,
				"statusCategory": map[string]string{"key": s.statuses[issue.Status]},
			},
		},
	})
}

func (s *Server) remoteLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GlobalID string `json:"globalId"`
		Object   struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Summary string `json:"summary"`
			Status  struct {
				Resolved bool `json:"resolved"`
			} `json:"status"`
		} `json:"object"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Object.URL == "" || body.Object.Title == "" {
		writeError(w, http.StatusBadRequest, "invalid remote link")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	issue, ok := s.lookup(w, r)
	if !ok {
		return
	}
	link := RemoteLink{
		GlobalID: body.GlobalID,
		URL:      body.Object.URL,
		Title:    body.Object.Title,
		Summary:  body.Object.Summary,
		Resolved: body.Object.Status.Resolved,
	}
	links := s.links[issue.Key]
	status := http.StatusCreated
	if i := slices.IndexFunc(links, func(l RemoteLink) bool { return l.GlobalID != "" && l.GlobalID == link.GlobalID }); i >= 0 {
		links[i] = link
		status = http.StatusOK
	} else {
		links = append(links, link)
	}
	s.links[issue.Key] = links
	writeJSON(w, status, map[string]any{"id": len(links), "self": s.URL + r.URL.Path})
}

// deleteRemoteLink removes the link of ?globalId=, 404 when there is none.
func (s *Server) deleteRemoteLink(w http.ResponseWriter, r *http.Request) {
	globalID := r.URL.Query().Get("globalId")
	if globalID == "" {
		writeError(w, http.StatusBadRequest, "globalId is required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	issue, ok := s.lookup(w, r)
	if !ok {
		return
	}
	links := s.links[issue.Key]
	i := slices.IndexFunc(links, func(l RemoteLink) bool { return l.GlobalID == globalID })
	if i < 0 {
		writeError(w, http.StatusNotFound, "No remote link with that global id.")
		return
	}
	s.links[issue.Key] = slices.Delete(links, i, i+1)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) comment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Body string `json:"body"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Body == "" {
		writeError(w, http.StatusBadRequest, "Comment body can not be empty!")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	issue, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.comments[issue.Key] = append(s.comments[issue.Key], body.Body)
	writeJSON(w, http.StatusCreated, map[string]any{"id": strconv.Itoa(len(s.comments[issue.Key])), "body": body.Body})
}

func (s *Server) transitions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	issue, ok := s.lookup(w, r)
	if !ok {
		return
	}
	list := []map[string]any{}
	for _, t := range s.workflow[issue.Status] {
		list = append(list, map[string]any{"id": t.ID, "name": "Move to " + t.To, "to": map[string]string{"name": t.To}})
	}
	writeJSON(w, http.StatusOK, map[string]any{"transitions": list})
}

func (s *Server) transition(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Transition struct {
			ID string `json:"id"`
		} `json:"transition"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeError(w, http.StatusBadRequest, "invalid transition")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	issue, ok := s.lookup(w, r)
	if !ok {
		return
	}
	for _, t := range s.workflow[issue.Status] {
		if t.ID == body.Transition.ID {
			issue.Status = t.To
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeError(w, http.StatusBadRequest, "Transition id '"+body.Transition.ID+"' is not valid for this issue.")
}

func (s *Server) picker(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
	s.mu.Lock()
	keys := make([]string, 0, len(s.issues))
	for key := range s.issues {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var found []map[string]string
	for _, key := range keys {
		issue := s.issues[key]
		if query == "" || strings.Contains(strings.ToLower(key), query) || strings.Contains(strings.ToLower(issue.Summary), query) {
			found = append(found, map[string]string{
				"key":         key,
				"summary":     "<b>" + issue.Summary + "</b>",
				"summaryText": issue.Summary,
			})
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"sections": []map[string]any{
		{"label": "History Search", "id": "hs", "issues": found},
		{"label": "Current Search", "id": "cs", "issues": found},
	}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"errorMessages": []string{msg}, "errors": map[string]string{}})
}
