package usecase

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	imail "github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/snow"
)

const maintenanceEventPrefix = "maintenance."

// MaintenanceEmailer queues maintenance announcements in the email outbox.
// They are service notices: they ignore the notification opt-out.
type MaintenanceEmailer struct {
	outbox  notifierOutbox
	node    snow.Node
	baseURL string
	kick    dispatcherKicker
	now     func() time.Time
}

func NewMaintenanceEmailer(outbox notifierOutbox, node snow.Node, baseURL string) *MaintenanceEmailer {
	return &MaintenanceEmailer{outbox: outbox, node: node, baseURL: baseURL, now: time.Now}
}

func (m *MaintenanceEmailer) WithKicker(kicker dispatcherKicker) *MaintenanceEmailer {
	m.kick = kicker
	return m
}

func (m *MaintenanceEmailer) Kick() {
	if m.kick != nil {
		m.kick.Kick()
	}
}

func (m *MaintenanceEmailer) Announce(ctx context.Context, a MaintenanceAnnouncement) (int, error) {
	recipients := dedupeMaintenanceRecipients(a.Recipients)
	if len(recipients) == 0 {
		return 0, nil
	}
	var projectID snow.ID
	if a.Window.ProjectID != nil {
		projectID = *a.Window.ProjectID
	}
	threadKey := "maintenance:" + a.Window.ID.Base36()
	seen, err := m.outbox.ThreadRecipients(ctx, projectID, threadKey)
	if err != nil {
		return 0, err
	}
	now := m.now()
	subject, text, htmlBody := m.render(a, now)
	host := m.messageHost()
	deliveries := make([]*domain.EmailDelivery, 0, len(recipients))
	for _, r := range recipients {
		id := m.node.Generate()
		root := fmt.Sprintf("<nipa-maintenance-%s-%s@%s>", a.Window.ID.Base36(), r.UserID.Base36(), host)
		msg := imail.Message{To: []string{r.Email}, Subject: subject, Text: text, HTML: htmlBody, MessageID: root}
		if _, ok := seen[r.UserID]; ok {
			msg.MessageID = fmt.Sprintf("<nipa-maintenance-%s-%s-%s@%s>", a.Window.ID.Base36(), r.UserID.Base36(), id.Base36(), host)
			msg.InReplyTo = root
			msg.References = []string{root}
		}
		body, err := imail.EncodeMessage(msg)
		if err != nil {
			return 0, err
		}
		deliveries = append(deliveries, &domain.EmailDelivery{
			ID:            id,
			Event:         maintenanceEventPrefix + string(a.Notice),
			ProjectID:     projectID,
			UserID:        r.UserID,
			Email:         r.Email,
			Subject:       subject,
			ThreadKey:     threadKey,
			Body:          body,
			State:         domain.EmailDeliveryPending,
			NextAttemptAt: &now,
		})
	}
	if err := m.outbox.Enqueue(ctx, deliveries); err != nil {
		return 0, err
	}
	return len(deliveries), nil
}

func dedupeMaintenanceRecipients(in []domain.MaintenanceRecipient) []domain.MaintenanceRecipient {
	ids := make(map[snow.ID]struct{}, len(in))
	emails := make(map[string]struct{}, len(in))
	out := make([]domain.MaintenanceRecipient, 0, len(in))
	for _, r := range in {
		r.Email = strings.TrimSpace(r.Email)
		email := strings.ToLower(r.Email)
		if !imail.ValidAddress(r.Email) {
			continue
		}
		if _, ok := ids[r.UserID]; ok {
			continue
		}
		if _, ok := emails[email]; ok {
			continue
		}
		ids[r.UserID], emails[email] = struct{}{}, struct{}{}
		out = append(out, r)
	}
	return out
}

var maintenanceModeTexts = map[string]string{
	string(maintenance.ReadOnly): "Read-only. You can sign in, browse, clone and update; pushes, locks, merge requests and other changes are refused.",
	string(maintenance.Full):     "Closed. You can sign in, but reading and changing data are refused.",
	string(maintenance.Frozen):   "Frozen while the database is moved. Signed-in sessions can still read; signing in and every change are refused.",
}

func (m *MaintenanceEmailer) render(a MaintenanceAnnouncement, now time.Time) (subject, text, htmlBody string) {
	w := a.Window
	scope := maintenanceScopeText(a)
	var lead string
	switch {
	case a.Notice == MaintenanceNoticeFinished:
		subject = "Maintenance finished: " + scope
		lead = fmt.Sprintf("Maintenance of %s has ended.", scope)
	case a.Notice == MaintenanceNoticeUpdated:
		subject = "Maintenance updated: " + scope
		lead = fmt.Sprintf("The maintenance of %s was updated.", scope)
	case !w.StartsAt.After(now):
		subject = "Maintenance started: " + scope
		lead = fmt.Sprintf("Maintenance of %s has started.", scope)
	default:
		subject = "Maintenance scheduled: " + scope
		lead = fmt.Sprintf("Maintenance of %s is scheduled.", scope)
	}
	paragraphs := []string{lead}
	if a.Notice != MaintenanceNoticeFinished {
		mode, ok := maintenanceModeTexts[w.Mode]
		if !ok {
			mode = w.Mode
		}
		end := "Estimated end: not announced."
		if w.EndsAt != nil {
			end = "Estimated end: " + maintenanceTime(*w.EndsAt) + "."
		}
		paragraphs = append(paragraphs,
			"Mode: "+mode,
			"Starts: "+maintenanceTime(w.StartsAt)+".",
			end+" The end is an estimate: maintenance ends when an administrator ends it.")
	}
	if msg := strings.TrimSpace(w.Message); msg != "" {
		paragraphs = append(paragraphs, "Message: "+msg)
	}
	link := m.link(w)
	var plain, rich strings.Builder
	for _, p := range paragraphs {
		plain.WriteString(p + "\n\n")
		fmt.Fprintf(&rich, "<p>%s</p>", html.EscapeString(p))
	}
	if link != "" {
		plain.WriteString("Open Nipa: " + link + "\n")
		fmt.Fprintf(&rich, `<p><a href="%s">Open Nipa</a></p>`, html.EscapeString(link))
	}
	return subject, plain.String(), rich.String()
}

func maintenanceScopeText(a MaintenanceAnnouncement) string {
	w := a.Window
	switch {
	case w.ProjectID != nil:
		return fmt.Sprintf("project %s/%s", w.OrgSlug, w.ProjectSlug)
	case w.OrgID != nil:
		if a.OrgName != "" && a.OrgName != w.OrgSlug {
			return fmt.Sprintf("organization %s (%s)", a.OrgName, w.OrgSlug)
		}
		return "organization " + w.OrgSlug
	default:
		return "the Nipa server"
	}
}

func maintenanceTime(t time.Time) string {
	return t.UTC().Format("Mon 2 Jan 2006 15:04 UTC")
}

func (m *MaintenanceEmailer) link(w domain.MaintenanceWindow) string {
	base := strings.TrimRight(strings.TrimSpace(m.baseURL), "/")
	switch {
	case base == "":
		return ""
	case w.ProjectID != nil:
		return base + "/" + url.PathEscape(w.OrgSlug) + "/" + url.PathEscape(w.ProjectSlug)
	case w.OrgID != nil:
		return base + "/" + url.PathEscape(w.OrgSlug)
	default:
		return base + "/"
	}
}

func (m *MaintenanceEmailer) messageHost() string {
	if parsed, err := url.Parse(m.baseURL); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return "nipa.local"
}
