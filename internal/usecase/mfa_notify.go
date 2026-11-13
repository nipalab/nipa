package usecase

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	imail "github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
)

const mfaNoticeEvent = "user.mfa"

// MFAMailer tells the account owner about changes to their second factor.
// These are security notices: they ignore the notification opt-out.
type MFAMailer struct {
	outbox  invitationOutbox
	node    snow.Node
	baseURL string
	kick    dispatcherKicker
	now     func() time.Time
}

func NewMFAMailer(outbox invitationOutbox, node snow.Node, baseURL string) *MFAMailer {
	return &MFAMailer{outbox: outbox, node: node, baseURL: baseURL, now: time.Now}
}

func (m *MFAMailer) WithKicker(kicker dispatcherKicker) *MFAMailer {
	m.kick = kicker
	return m
}

var mfaNoticeTexts = map[string]struct{ subject, body string }{
	MFANoticeEnabled: {
		"Two-factor authentication was turned on",
		"Two-factor authentication was turned on for your Nipa account. Signing in with a password now also asks for a code from your authenticator app.",
	},
	MFANoticeDisabled: {
		"Two-factor authentication was turned off",
		"Two-factor authentication was turned off for your Nipa account. Signing in with a password no longer asks for a code.",
	},
	MFANoticeResetByAdmin: {
		"An administrator turned off two-factor authentication",
		"An administrator turned off two-factor authentication for your Nipa account. Signing in with a password no longer asks for a code.",
	},
	MFANoticeRecoveryCodeUsed: {
		"A recovery code was used",
		"One of the recovery codes of your Nipa account was just used. Each code works once.",
	},
}

func (m *MFAMailer) NotifyMFA(ctx context.Context, user *domain.User, notice string) {
	text, ok := mfaNoticeTexts[notice]
	if !ok || user == nil || !imail.ValidAddress(user.Email) {
		return
	}
	if err := m.enqueue(ctx, user, text.subject, text.body); err != nil {
		obs.Log(ctx).Warn("queue two-factor notice failed", "user", user.ID.Base36(), "notice", notice, "error", err)
		return
	}
	if m.kick != nil {
		m.kick.Kick()
	}
}

func (m *MFAMailer) enqueue(ctx context.Context, user *domain.User, subject, body string) error {
	link := strings.TrimRight(m.baseURL, "/") + "/settings/profile?tab=security"
	warning := "If this was not you, change your password and contact your administrator."
	plain := fmt.Sprintf("%s\n\nReview your security settings:\n\n%s\n\n%s\n", body, link, warning)
	htmlBody := fmt.Sprintf(`<p>%s</p><p><a href="%s">Review your security settings</a></p><p>%s</p>`,
		html.EscapeString(body), html.EscapeString(link), html.EscapeString(warning))
	encoded, err := imail.EncodeMessage(imail.Message{
		To:      []string{user.Email},
		Subject: subject,
		Text:    plain,
		HTML:    htmlBody,
	})
	if err != nil {
		return err
	}
	now := m.now()
	return m.outbox.Enqueue(ctx, []*domain.EmailDelivery{{
		ID:            m.node.Generate(),
		Event:         mfaNoticeEvent,
		UserID:        user.ID,
		Email:         user.Email,
		Subject:       subject,
		Body:          encoded,
		State:         domain.EmailDeliveryPending,
		NextAttemptAt: &now,
	}})
}
