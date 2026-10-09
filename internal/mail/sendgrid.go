package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultSendGridEndpoint = "https://api.sendgrid.com"
	maxResponseSnippet      = 512
)

type sendGridSender struct {
	apiKey   string
	endpoint string
	from     address
	replyTo  *address
	client   *http.Client
}

type sendGridPayload struct {
	Personalizations []sendGridPersonalization `json:"personalizations"`
	From             sendGridAddress           `json:"from"`
	ReplyTo          *sendGridAddress          `json:"reply_to,omitempty"`
	Subject          string                    `json:"subject"`
	Content          []sendGridContent         `json:"content"`
}

type sendGridPersonalization struct {
	To      []sendGridAddress `json:"to"`
	Headers map[string]string `json:"headers,omitempty"`
}

type sendGridAddress struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type sendGridContent struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func newSendGridSender(cfg SendGridConfig, from, replyTo string, timeout time.Duration) (*sendGridSender, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("sendgrid sender: api key is required")
	}
	parsedFrom, err := parseAddress(from, "from")
	if err != nil {
		return nil, fmt.Errorf("sendgrid sender: %w", err)
	}
	var parsedReplyTo *address
	if strings.TrimSpace(replyTo) != "" {
		parsed, err := parseAddress(replyTo, "reply-to")
		if err != nil {
			return nil, fmt.Errorf("sendgrid sender: %w", err)
		}
		parsedReplyTo = &parsed
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultSendGridEndpoint
	}
	return &sendGridSender{
		apiKey:   cfg.APIKey,
		endpoint: strings.TrimRight(endpoint, "/"),
		from:     parsedFrom,
		replyTo:  parsedReplyTo,
		client:   &http.Client{Timeout: timeout},
	}, nil
}

func (s *sendGridSender) Send(ctx context.Context, msg Message) error {
	from, err := resolveFrom(msg, s.from)
	if err != nil {
		return err
	}
	to, err := parseAddresses(msg.To, "to")
	if err != nil {
		return err
	}
	replyTo, err := resolveReplyTo(msg, s.replyTo)
	if err != nil {
		return err
	}

	recipients := make([]sendGridAddress, 0, len(to))
	for _, addr := range to {
		recipients = append(recipients, sendGridAddress{Email: addr.Addr, Name: addr.Name})
	}
	personalization := sendGridPersonalization{To: recipients}
	headers := map[string]string{}
	if msg.MessageID != "" {
		headers["Message-ID"] = normalizeMessageID(msg.MessageID)
	}
	if msg.InReplyTo != "" {
		headers["In-Reply-To"] = normalizeMessageID(msg.InReplyTo)
	}
	if len(msg.References) > 0 {
		refs := make([]string, 0, len(msg.References))
		for _, ref := range msg.References {
			refs = append(refs, normalizeMessageID(ref))
		}
		headers["References"] = strings.Join(refs, " ")
	}
	if len(headers) > 0 {
		personalization.Headers = headers
	}

	payload := sendGridPayload{
		Personalizations: []sendGridPersonalization{personalization},
		From:             sendGridAddress{Email: from.Addr, Name: from.Name},
		Subject:          msg.Subject,
	}
	if replyTo != nil {
		payload.ReplyTo = &sendGridAddress{Email: replyTo.Addr, Name: replyTo.Name}
	}
	if msg.Text != "" {
		payload.Content = append(payload.Content, sendGridContent{Type: "text/plain", Value: msg.Text})
	}
	if msg.HTML != "" {
		payload.Content = append(payload.Content, sendGridContent{Type: "text/html", Value: msg.HTML})
	}
	if len(payload.Content) == 0 {
		payload.Content = append(payload.Content, sendGridContent{Type: "text/plain", Value: msg.Subject})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("sendgrid encode: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint+"/v3/mail/send", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sendgrid request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+s.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", UserAgent)

	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("sendgrid send: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("sendgrid response %s%s", response.Status, responseSnippet(response.Body))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseSnippet))
	return nil
}

func (s *sendGridSender) Close() error {
	s.client.CloseIdleConnections()
	return nil
}

func responseSnippet(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, maxResponseSnippet))
	if err != nil {
		return ""
	}
	snippet := strings.TrimSpace(string(data))
	if snippet == "" {
		return ""
	}
	return ": " + snippet
}
