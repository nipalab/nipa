package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
)

const (
	maintenanceProbeTimeout = 2 * time.Second
	maintenanceNoticeAhead  = 24 * time.Hour
)

// cloneMaintenance asks the server of the clone at root for the maintenance
// windows that cover it. Any failure is no answer.
func (c *Cli) cloneMaintenance(cmd *cobra.Command, root string) *domain.MaintenanceStatus {
	if c.useCase == nil || c.useCase.Version() == nil {
		return nil
	}
	cfg, err := localrepo.NewLocalRepoWithTarget(root).LoadConfig()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), maintenanceProbeTimeout)
	defer cancel()
	st, err := c.useCase.Version().Maintenance(ctx, cfg.Url)
	if err != nil || len(st.Windows()) == 0 {
		return nil
	}
	return st
}

func writeMaintenance(w io.Writer, st *domain.MaintenanceStatus) {
	for _, win := range st.Windows() {
		_, _ = fmt.Fprintf(w, "maintenance: %s\n", win.Describe())
	}
}

// maintenanceGate refuses a write before any work when an active window
// would refuse it, and announces a window that starts within a day.
func maintenanceGate(w io.Writer, st *domain.MaintenanceStatus, now time.Time) error {
	if refused := st.Refusal(); refused != nil {
		return refused
	}
	next := st.NextStart()
	if next == nil {
		return nil
	}
	if left := next.StartsAt.Sub(now); left <= maintenanceNoticeAhead {
		_, _ = fmt.Fprintf(w, "maintenance starts in %s: %s\n", roughDuration(left), next.Describe())
	}
	return nil
}

func roughDuration(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	s := d.Round(time.Minute).String()
	return strings.TrimSuffix(s, "0s")
}

// ErrorText renders err for the terminal. Maintenance refusals add the
// estimated end and the hint.
func ErrorText(err error) string {
	var de *domain.Error
	if errors.As(err, &de) && de.Maintenance != nil {
		return de.Maintenance.Describe(err.Error(), de.Hint)
	}
	return err.Error()
}
