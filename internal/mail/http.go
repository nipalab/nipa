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
	"text/template"
	"time"
)

const defaultHTTPBodyTemplate = `{"from":{{json .From}},"from_email":{{json .FromEmail}},"to":{{json .To}},"subject":{{json .Subject}},"text":{{json .Text}},"html":{{json .HTML}}}`

type httpSender struct {
	endpoint    string
	method      string
	contentType string
	headers     map[string]string
	body        *template.Template
	from        address
	replyTo     *address
	client      *http.Client
}

type httpTemplateData struct {
	From       string
	FromName   string
	FromEmail  string
	ReplyTo    string
	To         []string
	ToHeader   string
	Subject    string
	Text       string
	HTML       string
	MessageID  string
	InReplyTo  string
	References []string
}

func newHTTPSender(cfg HTTPConfig, from, replyTo string, timeout time.Duration) (*httpSender, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("http sender: endpoint is required")
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || (parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https") || parsedEndpoint.Host == "" {
		return nil, fmt.Errorf("http sender: invalid endpoint %q", endpoint)
	}
	parsedFrom, err := parseAddress(from, "from")
	if err != nil {
		return nil, fmt.Errorf("http sender: %w", err)
	}
	var parsedReplyTo *address
	if strings.TrimSpace(replyTo) != "" {
		parsed, err := parseAddress(replyTo, "reply-to")
		if err != nil {
			return nil, fmt.Errorf("http sender: %w", err)
		}
		parsedReplyTo = &parsed
	}
	headers, err := parseHeadersJSON(cfg.HeadersJSON)
	if err != nil {
		return nil, fmt.Errorf("http sender: %w", err)
	}
	rawTemplate := cfg.BodyTemplate
	if strings.TrimSpace(rawTemplate) == "" {
		rawTemplate = defaultHTTPBodyTemplate
	}
	body, err := template.New("body").Funcs(template.FuncMap{"json": jsonString}).Parse(rawTemplate)
	if err != nil {
		return nil, fmt.Errorf("http sender: invalid body template: %w", err)
	}
	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodPost
	}
	contentType := strings.TrimSpace(cfg.ContentType)
	if contentType == "" {
		contentType = "application/json"
	}
	return &httpSender{
		endpoint:    endpoint,
		method:      method,
		contentType: contentType,
		headers:     headers,
		body:        body,
		from:        parsedFrom,
		replyTo:     parsedReplyTo,
		client:      &http.Client{Timeout: timeout},
	}, nil
}

func (s *httpSender) Send(ctx context.Context, msg Message) error {
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

	toEmails := make([]string, 0, len(to))
	for _, addr := range to {
		toEmails = append(toEmails, addr.Addr)
	}
	data := httpTemplateData{
		From:      formatAddress(from),
		FromName:  from.Name,
		FromEmail: from.Addr,
		To:        toEmails,
		ToHeader:  strings.Join(toEmails, ", "),
		Subject:   msg.Subject,
		Text:      msg.Text,
		HTML:      msg.HTML,
	}
	if msg.MessageID != "" {
		data.MessageID = normalizeMessageID(msg.MessageID)
	}
	if msg.InReplyTo != "" {
		data.InReplyTo = normalizeMessageID(msg.InReplyTo)
	}
	if len(msg.References) > 0 {
		refs := make([]string, 0, len(msg.References))
		for _, ref := range msg.References {
			refs = append(refs, normalizeMessageID(ref))
		}
		data.References = refs
	}
	if replyTo != nil {
		data.ReplyTo = formatAddress(*replyTo)
	}

	var body bytes.Buffer
	if err := s.body.Execute(&body, data); err != nil {
		return fmt.Errorf("http sender: render body: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, s.method, s.endpoint, &body)
	if err != nil {
		return fmt.Errorf("http sender: request: %w", err)
	}
	request.Header.Set("Content-Type", s.contentType)
	request.Header.Set("User-Agent", UserAgent)
	for name, value := range s.headers {
		request.Header.Set(name, value)
	}

	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("http sender: send: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("http sender: response %s%s", response.Status, responseSnippet(response.Body))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseSnippet))
	return nil
}

func (s *httpSender) Close() error {
	s.client.CloseIdleConnections()
	return nil
}

func jsonString(value any) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}
