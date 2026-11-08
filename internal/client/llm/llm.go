// Package llm is a minimal client for OpenAI-compatible chat completion
// endpoints (Ollama, vLLM, llama.cpp, OpenAI and compatible clouds) used by
// the CLI --generate flags. It sends one request per call, without streaming.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/client/aiprompt"
	"github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/domain"
)

const (
	DefaultTimeout   = 60 * time.Second
	maxResponseBytes = 1 << 20
	maxErrorText     = 300
	temperature      = 0.2
)

const configHint = "set NIPA_LLM_ENDPOINT and NIPA_LLM_MODEL or llm in ~/.config/nipa/config.json"

type Client struct {
	url     string
	model   string
	key     string
	timeout time.Duration
	http    *http.Client
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.timeout = d }
}

// New validates the settings. The request URL is the configured endpoint
// with a trailing slash removed and /chat/completions appended, so the
// endpoint is the API base (http://localhost:11434/v1 for Ollama).
func New(s config.LLMSettings, opts ...Option) (*Client, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(s.Endpoint), "/")
	model := strings.TrimSpace(s.Model)
	if endpoint == "" || model == "" {
		return nil, domain.NewUserError("no LLM endpoint configured for --generate: "+configHint).WithHint(configHint, "")
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, domain.NewUserError("the LLM endpoint must be an http(s) URL such as http://localhost:11434/v1").WithHint(configHint, "")
	}
	c := &Client{url: endpoint + "/chat/completions", model: model, key: s.APIKey, timeout: DefaultTimeout, http: http.DefaultClient}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

func (c *Client) Model() string { return c.model }

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	Temperature float64   `json:"temperature"`
	Stream      bool      `json:"stream"`
}

type response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Complete sends the prompt and returns the first choice's text, trimmed.
func (c *Client) Complete(ctx context.Context, p aiprompt.Prompt) (string, error) {
	body, err := json.Marshal(request{
		Model:       c.model,
		Messages:    []message{{Role: "system", Content: p.System}, {Role: "user", Content: p.User}},
		Temperature: temperature,
	})
	if err != nil {
		return "", err
	}
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", c.requestError(ctx, reqCtx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", c.requestError(ctx, reqCtx, err)
	}
	if len(data) > maxResponseBytes {
		return "", domain.NewUserError("the LLM response is larger than 1 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", domain.NewUserError(fmt.Sprintf("LLM endpoint answered %d: %s", resp.StatusCode, c.redact(errorText(data, resp.Status))))
	}
	var out response
	if err := json.Unmarshal(data, &out); err != nil {
		return "", domain.NewUserError("the LLM endpoint returned an invalid response (expected an OpenAI-compatible chat completion)")
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", domain.NewUserError("the LLM endpoint returned no completion")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

func (c *Client) requestError(parent, reqCtx context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
		return domain.NewUserError(fmt.Sprintf("the LLM request timed out after %s", c.timeout))
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	return domain.NewUserError("the LLM request failed: " + c.redact(err.Error()))
}

func (c *Client) redact(s string) string {
	if c.key == "" {
		return s
	}
	return strings.ReplaceAll(s, c.key, "[redacted]")
}

// errorText extracts the server message from an OpenAI-style error body
// ({"error":{"message":...}}, {"error":"..."} or {"message":...}), falling
// back to the trimmed raw body or the status line.
func errorText(data []byte, status string) string {
	var parsed struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	msg := ""
	if json.Unmarshal(data, &parsed) == nil {
		var nested struct {
			Message string `json:"message"`
		}
		var plain string
		switch {
		case json.Unmarshal(parsed.Error, &nested) == nil && nested.Message != "":
			msg = nested.Message
		case json.Unmarshal(parsed.Error, &plain) == nil && plain != "":
			msg = plain
		default:
			msg = parsed.Message
		}
	}
	if msg == "" {
		msg = string(data)
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if msg == "" {
		return status
	}
	if len(msg) > maxErrorText {
		msg = strings.ToValidUTF8(msg[:maxErrorText], "") + "…"
	}
	return msg
}
