package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	imail "github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/snow"
)

func newTestMaintenanceEmailer(t *testing.T, baseURL string) (*MaintenanceEmailer, *MocknotifierOutbox, *fakeKicker) {
	t.Helper()
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	outbox := NewMocknotifierOutbox(gomock.NewController(t))
	kicker := &fakeKicker{}
	m := NewMaintenanceEmailer(outbox, node, baseURL).WithKicker(kicker)
	m.now = func() time.Time { return maintNow }
	return m, outbox, kicker
}

func captureDeliveries(outbox *MocknotifierOutbox, into *[]*domain.EmailDelivery) {
	outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ds []*domain.EmailDelivery) error {
		*into = ds
		return nil
	})
}

func decodeDelivery(t *testing.T, d *domain.EmailDelivery) imail.Message {
	t.Helper()
	msg, err := imail.DecodeMessage(d.Body)
	require.NoError(t, err)
	return msg
}

func TestMaintenanceEmailer_ServerScheduled(t *testing.T) {
	m, outbox, kicker := newTestMaintenanceEmailer(t, "https://nipa.example/")
	start := time.Date(2026, 11, 20, 22, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	w := domain.MaintenanceWindow{ID: 77, Mode: "read_only", Message: "Disk <swap>", StartsAt: start, EndsAt: &end, Notify: true}
	outbox.EXPECT().ThreadRecipients(gomock.Any(), snow.ID(0), "maintenance:"+snow.ID(77).Base36()).Return(nil, nil)
	var got []*domain.EmailDelivery
	captureDeliveries(outbox, &got)

	n, err := m.Announce(context.Background(), MaintenanceAnnouncement{
		Notice: MaintenanceNoticeScheduled,
		Window: w,
		Recipients: []domain.MaintenanceRecipient{
			{UserID: 1, Email: "ann@example.com"},
			{UserID: 1, Email: "ann@example.com"},
			{UserID: 2, Email: " ANN@example.com "},
			{UserID: 3, Email: "not an address"},
			{UserID: 4, Email: "bob@example.com"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Len(t, got, 2)
	require.Zero(t, kicker.kicks, "the usecase kicks after the commit")
	m.Kick()
	require.Equal(t, 1, kicker.kicks)

	d := got[0]
	require.Equal(t, "maintenance.scheduled", d.Event)
	require.Equal(t, snow.ID(1), d.UserID)
	require.Equal(t, "ann@example.com", d.Email)
	require.Zero(t, d.ProjectID)
	require.Equal(t, domain.EmailDeliveryPending, d.State)
	require.Equal(t, "Maintenance scheduled: the Nipa server", d.Subject)
	require.Equal(t, snow.ID(4), got[1].UserID)

	msg := decodeDelivery(t, d)
	require.Equal(t, []string{"ann@example.com"}, msg.To)
	require.Equal(t, "<nipa-maintenance-"+snow.ID(77).Base36()+"-1@nipa.example>", msg.MessageID)
	require.Empty(t, msg.InReplyTo)
	require.Contains(t, msg.Text, "Maintenance of the Nipa server is scheduled.")
	require.Contains(t, msg.Text, "Mode: Read-only.")
	require.Contains(t, msg.Text, "Starts: Fri 20 Nov 2026 22:00 UTC.")
	require.Contains(t, msg.Text, "Estimated end: Fri 20 Nov 2026 23:00 UTC. The end is an estimate")
	require.Contains(t, msg.Text, "Message: Disk <swap>")
	require.Contains(t, msg.Text, "Open Nipa: https://nipa.example/\n")
	require.Contains(t, msg.HTML, "Disk &lt;swap&gt;")
	require.Contains(t, msg.HTML, `<a href="https://nipa.example/">Open Nipa</a>`)
}

func TestMaintenanceEmailer_ProjectUpdateReplies(t *testing.T) {
	m, outbox, _ := newTestMaintenanceEmailer(t, "")
	project := snow.ID(20)
	w := domain.MaintenanceWindow{ID: 9, ProjectID: &project, OrgSlug: "acme", ProjectSlug: "game", Mode: "full", StartsAt: maintNow.Add(-time.Minute)}
	outbox.EXPECT().ThreadRecipients(gomock.Any(), project, "maintenance:9").Return(map[snow.ID]struct{}{5: {}}, nil)
	var got []*domain.EmailDelivery
	captureDeliveries(outbox, &got)

	n, err := m.Announce(context.Background(), MaintenanceAnnouncement{
		Notice:     MaintenanceNoticeUpdated,
		Window:     w,
		Recipients: []domain.MaintenanceRecipient{{UserID: 5, Email: "o@example.com"}, {UserID: 6, Email: "p@example.com"}},
	})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, project, got[0].ProjectID)
	require.Equal(t, "maintenance.updated", got[0].Event)
	require.Equal(t, "Maintenance updated: project acme/game", got[0].Subject)

	reply := decodeDelivery(t, got[0])
	root := "<nipa-maintenance-9-5@nipa.local>"
	require.Equal(t, root, reply.InReplyTo)
	require.Equal(t, []string{root}, reply.References)
	require.NotEqual(t, root, reply.MessageID)
	require.Contains(t, reply.Text, "Mode: Closed.")
	require.Contains(t, reply.Text, "Estimated end: not announced.")
	require.NotContains(t, reply.Text, "Open Nipa")
	require.NotContains(t, reply.Text, "Message:")

	first := decodeDelivery(t, got[1])
	require.Equal(t, "<nipa-maintenance-9-6@nipa.local>", first.MessageID)
	require.Empty(t, first.InReplyTo)
}

func TestMaintenanceEmailer_Subjects(t *testing.T) {
	org := snow.ID(10)
	project := snow.ID(20)
	cases := []struct {
		name    string
		a       MaintenanceAnnouncement
		subject string
		text    string
		link    string
	}{
		{
			name:    "org started with name",
			a:       MaintenanceAnnouncement{Notice: MaintenanceNoticeScheduled, OrgName: "Acme Games", Window: domain.MaintenanceWindow{ID: 1, OrgID: &org, OrgSlug: "acme", Mode: "full", StartsAt: maintNow}},
			subject: "Maintenance started: organization Acme Games (acme)",
			text:    "Maintenance of organization Acme Games (acme) has started.",
			link:    "https://nipa.example/acme\n",
		},
		{
			name:    "org without a distinct name",
			a:       MaintenanceAnnouncement{Notice: MaintenanceNoticeScheduled, OrgName: "acme", Window: domain.MaintenanceWindow{ID: 1, OrgID: &org, OrgSlug: "acme", Mode: "full", StartsAt: maintNow.Add(time.Hour)}},
			subject: "Maintenance scheduled: organization acme",
			text:    "Mode: Closed.",
			link:    "https://nipa.example/acme\n",
		},
		{
			name:    "project finished",
			a:       MaintenanceAnnouncement{Notice: MaintenanceNoticeFinished, Window: domain.MaintenanceWindow{ID: 1, ProjectID: &project, OrgSlug: "acme", ProjectSlug: "game", Mode: "read_only", Message: "done", StartsAt: maintNow}},
			subject: "Maintenance finished: project acme/game",
			text:    "Maintenance of project acme/game has ended.\n\nMessage: done\n\n",
			link:    "https://nipa.example/acme/game\n",
		},
		{
			name:    "server frozen",
			a:       MaintenanceAnnouncement{Notice: MaintenanceNoticeScheduled, Window: domain.MaintenanceWindow{ID: 1, Mode: "frozen", StartsAt: maintNow.Add(time.Hour)}},
			subject: "Maintenance scheduled: the Nipa server",
			text:    "Mode: Frozen while the database is moved.",
			link:    "https://nipa.example/\n",
		},
		{
			name:    "unknown mode is shown as is",
			a:       MaintenanceAnnouncement{Notice: MaintenanceNoticeScheduled, Window: domain.MaintenanceWindow{ID: 1, Mode: "odd", StartsAt: maintNow.Add(time.Hour)}},
			subject: "Maintenance scheduled: the Nipa server",
			text:    "Mode: odd\n",
			link:    "https://nipa.example/\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, outbox, _ := newTestMaintenanceEmailer(t, "https://nipa.example")
			outbox.EXPECT().ThreadRecipients(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
			var got []*domain.EmailDelivery
			captureDeliveries(outbox, &got)
			tc.a.Recipients = []domain.MaintenanceRecipient{{UserID: 1, Email: "a@example.com"}}
			_, err := m.Announce(context.Background(), tc.a)
			require.NoError(t, err)
			require.Equal(t, tc.subject, got[0].Subject)
			msg := decodeDelivery(t, got[0])
			require.Contains(t, msg.Text, tc.text)
			require.Contains(t, msg.Text, "Open Nipa: "+tc.link)
			if tc.a.Notice == MaintenanceNoticeFinished {
				require.NotContains(t, msg.Text, "Mode:")
			}
		})
	}
}

func TestMaintenanceEmailer_Failures(t *testing.T) {
	ctx := context.Background()
	recipients := []domain.MaintenanceRecipient{{UserID: 1, Email: "a@example.com"}}

	t.Run("nobody deliverable touches nothing", func(t *testing.T) {
		m, _, _ := newTestMaintenanceEmailer(t, "")
		n, err := m.Announce(ctx, MaintenanceAnnouncement{Recipients: []domain.MaintenanceRecipient{{UserID: 1, Email: "bad"}}})
		require.NoError(t, err)
		require.Zero(t, n)
	})
	t.Run("thread lookup error", func(t *testing.T) {
		m, outbox, _ := newTestMaintenanceEmailer(t, "")
		outbox.EXPECT().ThreadRecipients(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("db"))
		_, err := m.Announce(ctx, MaintenanceAnnouncement{Recipients: recipients})
		require.EqualError(t, err, "db")
	})
	t.Run("enqueue error", func(t *testing.T) {
		m, outbox, _ := newTestMaintenanceEmailer(t, "")
		outbox.EXPECT().ThreadRecipients(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
		outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(errors.New("db"))
		n, err := m.Announce(ctx, MaintenanceAnnouncement{Recipients: recipients})
		require.EqualError(t, err, "db")
		require.Zero(t, n)
	})
	t.Run("kick without a kicker", func(t *testing.T) {
		node, err := snow.NewNode(1)
		require.NoError(t, err)
		NewMaintenanceEmailer(nil, node, "").Kick()
	})
}
