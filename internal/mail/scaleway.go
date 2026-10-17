package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultScalewayEndpoint = "https://api.scaleway.com"
	defaultScalewayRegion   = "fr-par"
)

type scalewaySender struct {
	apiKey    string
	projectID string
	region    string
	endpoint  string
	from      address
	replyTo   *address
	client    *http.Client
}

type scalewayPayload struct {
	From              scalewayAddress   `json:"from"`
	To                []scalewayAddress `json:"to"`
	Subject           string            `json:"subject,omitempty"`
	Text              string            `json:"text,omitempty"`
	HTML              string            `json:"html,omitempty"`
	ProjectID         string            `json:"project_id"`
	AdditionalHeaders []scalewayHeader  `json:"additional_headers,omitempty"`
}

type scalewayAddress struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type scalewayHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func newScalewaySender(cfg ScalewayConfig, from, replyTo string, timeout time.Duration) (*scalewaySender, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("scaleway sender: api key is required")
	}
	if strings.TrimSpace(cfg.ProjectID) == "" {
		return nil, fmt.Errorf("scaleway sender: project id is required")
	}
	parsedFrom, err := parseAddress(from, "from")
	if err != nil {
		return nil, fmt.Errorf("scaleway sender: %w", err)
	}
	var parsedReplyTo *address
	if strings.TrimSpace(replyTo) != "" {
		parsed, err := parseAddress(replyTo, "reply-to")
		if err != nil {
			return nil, fmt.Errorf("scaleway sender: %w", err)
		}
		parsedReplyTo = &parsed
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultScalewayEndpoint
	}
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = defaultScalewayRegion
	}
	return &scalewaySender{
		apiKey:    cfg.APIKey,
		projectID: cfg.ProjectID,
		region:    region,
		endpoint:  strings.TrimRight(endpoint, "/"),
		from:      parsedFrom,
		replyTo:   parsedReplyTo,
		client:    &http.Client{Timeout: timeout},
	}, nil
}

func (s *scalewaySender) Send(ctx context.Context, msg Message) error {
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

	recipients := make([]scalewayAddress, 0, len(to))
	for _, addr := range to {
		recipients = append(recipients, scalewayAddress{Email: addr.Addr, Name: addr.Name})
	}
	payload := scalewayPayload{
		From:      scalewayAddress{Email: from.Addr, Name: from.Name},
		To:        recipients,
		Subject:   msg.Subject,
		ProjectID: s.projectID,
	}
	if replyTo != nil {
		payload.AdditionalHeaders = append(payload.AdditionalHeaders, scalewayHeader{Key: "Reply-To", Value: formatAddress(*replyTo)})
	}
	headers := []scalewayHeader{{Key: "Message-ID", Value: msg.MessageID}, {Key: "In-Reply-To", Value: msg.InReplyTo}}
	for _, header := range headers {
		if strings.TrimSpace(header.Value) == "" {
			continue
		}
		id, err := normalizeMessageID(header.Value)
		if err != nil {
			return err
		}
		payload.AdditionalHeaders = append(payload.AdditionalHeaders, scalewayHeader{Key: header.Key, Value: id})
	}
	if len(msg.References) > 0 {
		refs := make([]string, 0, len(msg.References))
		for _, ref := range msg.References {
			id, err := normalizeMessageID(ref)
			if err != nil {
				return err
			}
			refs = append(refs, id)
		}
		payload.AdditionalHeaders = append(payload.AdditionalHeaders, scalewayHeader{Key: "References", Value: strings.Join(refs, " ")})
	}
	payload.Text = msg.Text
	payload.HTML = msg.HTML
	if payload.Text == "" && payload.HTML == "" {
		payload.Text = msg.Subject
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("scaleway encode: %w", err)
	}
	endpoint := fmt.Sprintf("%s/transactional-email/v1alpha1/regions/%s/emails", s.endpoint, url.PathEscape(s.region))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("scaleway request: %w", err)
	}
	request.Header.Set("X-Auth-Token", s.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", UserAgent)

	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("scaleway send: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("scaleway response %s%s", response.Status, responseSnippet(response.Body))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseSnippet))
	return nil
}

func (s *scalewaySender) Close() error {
	s.client.CloseIdleConnections()
	return nil
}
