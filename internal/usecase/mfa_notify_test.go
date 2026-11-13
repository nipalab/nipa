package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestMFAMailer(t *testing.T) {
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	user := &domain.User{ID: 7, Email: "ann@example.com", NotifyEmail: false}

	for notice, subject := range map[string]string{
		MFANoticeEnabled:          "Two-factor authentication was turned on",
		MFANoticeDisabled:         "Two-factor authentication was turned off",
		MFANoticeResetByAdmin:     "An administrator turned off two-factor authentication",
		MFANoticeRecoveryCodeUsed: "A recovery code was used",
	} {
		t.Run(notice, func(t *testing.T) {
			outbox := NewMockinvitationOutbox(gomock.NewController(t))
			kicker := &fakeKicker{}
			outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ds []*domain.EmailDelivery) error {
				require.Len(t, ds, 1)
				d := ds[0]
				require.Equal(t, mfaNoticeEvent, d.Event)
				require.Equal(t, user.ID, d.UserID)
				require.Equal(t, user.Email, d.Email)
				require.Equal(t, subject, d.Subject)
				require.Equal(t, domain.EmailDeliveryPending, d.State)
				require.Contains(t, string(d.Body), "https://nipa.example/settings/profile?tab=security")
				return nil
			})
			NewMFAMailer(outbox, node, "https://nipa.example/").WithKicker(kicker).NotifyMFA(context.Background(), user, notice)
			require.Equal(t, 1, kicker.kicks)
		})
	}

	t.Run("skips unknown notices and bad addresses", func(t *testing.T) {
		outbox := NewMockinvitationOutbox(gomock.NewController(t))
		m := NewMFAMailer(outbox, node, "https://nipa.example")
		m.NotifyMFA(context.Background(), user, "other")
		m.NotifyMFA(context.Background(), &domain.User{ID: 8, Email: "not an address"}, MFANoticeEnabled)
		m.NotifyMFA(context.Background(), nil, MFANoticeEnabled)
	})

	t.Run("enqueue failure is only logged", func(t *testing.T) {
		outbox := NewMockinvitationOutbox(gomock.NewController(t))
		kicker := &fakeKicker{}
		outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(errors.New("db down"))
		NewMFAMailer(outbox, node, "https://nipa.example").WithKicker(kicker).NotifyMFA(context.Background(), user, MFANoticeEnabled)
		require.Zero(t, kicker.kicks)
	})
}
