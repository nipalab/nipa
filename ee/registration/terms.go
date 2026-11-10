package registration

import (
	"context"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	TermsSourceRegistration = "registration"
	TermsSourceApp          = "app"
)

// TermsStatus tells the SPA which terms version is current, what the user last
// accepted and whether the app must ask before going on.
type TermsStatus struct {
	Version  string
	Accepted *domain.TermsAcceptance
	Required bool
}

func errTermsRequired() error {
	return domain.NewErrorUser("you must accept the terms of service and privacy policy")
}

// TermsVersion is the published version users accept; empty disables the
// acceptance step.
func (r *Registration) TermsVersion() string {
	return strings.TrimSpace(r.terms())
}

func (r *Registration) TermsStatus(ctx context.Context, userID snow.ID) (*TermsStatus, error) {
	status := &TermsStatus{Version: r.TermsVersion()}
	if status.Version == "" {
		return status, nil
	}
	accepted, err := r.repo.LatestTermsAcceptance(ctx, userID)
	switch {
	case err == nil:
		status.Accepted = accepted
	case !domain.IsErrorNotFound(err):
		return nil, err
	}
	status.Required = status.Accepted == nil
	return status, nil
}

// AcceptTerms records that the signed-in user accepted the given version,
// which must be the current one so a stale page cannot accept old text.
func (r *Registration) AcceptTerms(ctx context.Context, userID snow.ID, version string) error {
	current := r.TermsVersion()
	if current == "" {
		return domain.NewErrorNotFound("terms acceptance is not enabled")
	}
	if version != current {
		return domain.NewErrorConflict("the terms were updated; reload the page and review the current version")
	}
	return r.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := r.recordTerms(ctx, userID, current, TermsSourceApp); err != nil {
			return err
		}
		if r.audit == nil {
			return nil
		}
		return r.audit.Record(ctx, domain.AuditEntry{
			Action:     domain.AuditUserTermsAccepted,
			TargetType: domain.AuditTargetUser,
			TargetID:   userID.Base36(),
			Metadata:   map[string]any{"version": version, "source": TermsSourceApp},
		})
	})
}

func (r *Registration) recordTerms(ctx context.Context, userID snow.ID, version, source string) error {
	return r.repo.RecordTermsAcceptance(ctx, domain.TermsAcceptance{
		UserID:     userID,
		Version:    version,
		Source:     source,
		AcceptedAt: r.now(),
	})
}
