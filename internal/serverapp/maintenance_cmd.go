package serverapp

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

// MaintenanceOpener opens the server database for the maintenance command
// without running migrations. Org, Project, PBAC, Audit and Maintenance must
// be set in the returned repositories; Email enables --notify.
type MaintenanceOpener func(cfg *config.Config) (usecase.Repositories, *sql.DB, error)

const maintenanceUsage = `usage:
  maintenance status
  maintenance set <read_only|full|frozen> [--org <slug> | --project <org/project>] [-m <message>] [--start <RFC3339>] [--end <RFC3339>] [--notify]
  maintenance end [--org <slug> | --project <org/project>]`

var errMaintenanceSchema = errors.New("maintenance_windows table not found: schema too old, start the server once")

// RunMaintenanceCommand handles `maintenance ...` before any server startup
// work. handled is false when args are not a maintenance command.
func RunMaintenanceCommand(args []string, open MaintenanceOpener, out io.Writer) (handled bool, err error) {
	if len(args) == 0 || args[0] != "maintenance" {
		return false, nil
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return true, err
	}
	return true, runMaintenanceCommand(context.Background(), cfg, args[1:], open, out)
}

type maintenanceFlags struct {
	org, project, message, start, end string
	notify                            bool
}

func runMaintenanceCommand(ctx context.Context, cfg *config.Config, args []string, open MaintenanceOpener, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(maintenanceUsage)
	}
	sub, rest := args[0], args[1:]
	var mode string
	fs := flag.NewFlagSet("maintenance "+sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var f maintenanceFlags
	fs.StringVar(&f.org, "org", "", "organization slug")
	fs.StringVar(&f.project, "project", "", "org/project")
	switch sub {
	case "status":
	case "set":
		fs.StringVar(&f.message, "m", "", "message shown to users")
		fs.StringVar(&f.start, "start", "", "start time (RFC3339)")
		fs.StringVar(&f.end, "end", "", "estimated end (RFC3339)")
		fs.BoolVar(&f.notify, "notify", false, "announce the window by email")
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			mode, rest = rest[0], rest[1:]
		}
	case "end":
	default:
		return fmt.Errorf("unknown maintenance command %q\n%s", sub, maintenanceUsage)
	}
	if err := fs.Parse(rest); err != nil {
		return fmt.Errorf("%w\n%s", err, maintenanceUsage)
	}
	if sub == "set" && mode == "" && fs.NArg() > 0 {
		mode = fs.Arg(0)
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return fmt.Errorf("%w\n%s", err, maintenanceUsage)
		}
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q\n%s", fs.Arg(0), maintenanceUsage)
	}
	if sub == "set" && mode == "" {
		return fmt.Errorf("a mode is required\n%s", maintenanceUsage)
	}
	if sub == "status" && (f.org != "" || f.project != "") {
		return fmt.Errorf("status takes no scope\n%s", maintenanceUsage)
	}
	if f.org != "" && f.project != "" {
		return errors.New("use either --org or --project")
	}
	var orgSlug, projectSlug string
	if f.project != "" {
		var ok bool
		orgSlug, projectSlug, ok = strings.Cut(f.project, "/")
		if !ok || orgSlug == "" || projectSlug == "" || strings.Contains(projectSlug, "/") {
			return fmt.Errorf("--project must be <org>/<project>, got %q", f.project)
		}
	}
	in := usecase.MaintenanceInput{Mode: mode, Message: f.message, Notify: f.notify}
	var err error
	if in.StartsAt, err = parseCLITime("--start", f.start); err != nil {
		return err
	}
	if in.EndsAt, err = parseCLITime("--end", f.end); err != nil {
		return err
	}

	uc, closeDB, err := openMaintenance(ctx, cfg, open, out, sub == "end" || f.notify)
	if err != nil {
		return err
	}
	defer closeDB()

	switch sub {
	case "status":
		state, err := uc.AdminStatus(ctx)
		if err != nil {
			return err
		}
		printMaintenanceState(out, state)
		return nil
	case "set":
		var st *maintenance.Status
		switch {
		case projectSlug != "":
			st, err = uc.SetProject(ctx, orgSlug, projectSlug, in)
		case f.org != "":
			st, err = uc.SetOrg(ctx, f.org, in)
		default:
			st, err = uc.SetServer(ctx, in)
		}
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "maintenance set: %s\n", describeStatus(scopeLabel(f.org, f.project), *st))
		if f.notify {
			printNotified(out, *st)
		}
		return nil
	default:
		switch {
		case projectSlug != "":
			err = uc.EndProject(ctx, orgSlug, projectSlug)
		case f.org != "":
			err = uc.EndOrg(ctx, f.org)
		default:
			err = uc.EndServer(ctx)
		}
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "maintenance ended: %s\n", scopeLabel(f.org, f.project))
		return nil
	}
}

func openMaintenance(ctx context.Context, cfg *config.Config, open MaintenanceOpener, out io.Writer, mailer bool) (*usecase.Maintenance, func(), error) {
	repos, dbConn, err := open(cfg)
	if err != nil {
		return nil, nil, err
	}
	closeDB := func() { _ = dbConn.Close() }
	if err := checkMaintenanceSchema(ctx, dbConn); err != nil {
		closeDB()
		return nil, nil, err
	}
	node, err := snow.NewNode(snow.ReservedCLINode)
	if err != nil {
		closeDB()
		return nil, nil, err
	}
	svc, poller := NewMaintenance(cfg, repos.Maintenance, nil)
	if err := poller.Poll(ctx); err != nil {
		_, _ = fmt.Fprintf(out, "warning: %v\n", err)
	}
	audit := usecase.NewAudit(repos.Audit, node, dbtx.NewTransactor(dbConn)).WithProjectOrgs(repos.PBAC)
	uc := usecase.NewMaintenance(repos.Maintenance, usecase.NewCommon(repos.Org, repos.Project), nil, nil, svc, node).
		WithAudit(audit).WithSystemActor()
	if mailer && repos.Email != nil && emailEnabled(cfg, out) {
		uc.WithMailer(usecase.NewMaintenanceEmailer(repos.Email, node, cfg.EmailBaseURL))
	}
	return uc, closeDB, nil
}

func emailEnabled(cfg *config.Config, out io.Writer) bool {
	sender, err := mail.NewFromConfig(cfg.MailSenderConfig())
	if err != nil {
		_, _ = fmt.Fprintf(out, "warning: email disabled: %v\n", err)
		return false
	}
	if sender == nil {
		return false
	}
	_ = sender.Close()
	return true
}

func checkMaintenanceSchema(ctx context.Context, dbConn *sql.DB) error {
	var n int64
	err := dbConn.QueryRowContext(ctx, "SELECT COUNT(*) FROM maintenance_windows").Scan(&n)
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no such table") || strings.Contains(msg, "does not exist") {
		return errMaintenanceSchema
	}
	return fmt.Errorf("read maintenance windows: %w", err)
}

func parseCLITime(name, raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be RFC3339 (e.g. 2026-11-20T22:00:00Z): %w", name, err)
	}
	return &t, nil
}

func scopeLabel(org, project string) string {
	switch {
	case project != "":
		return "project " + project
	case org != "":
		return "organization " + org
	default:
		return "server"
	}
}

func describeStatus(label string, st maintenance.Status) string {
	state := "scheduled"
	if st.Active {
		state = "active"
	}
	s := fmt.Sprintf("%s %s (%s", label, st.Mode, state)
	if !st.StartsAt.IsZero() {
		s += ", from " + st.StartsAt.UTC().Format(time.RFC3339)
	}
	if st.EndsAt != nil {
		s += ", until about " + st.EndsAt.UTC().Format(time.RFC3339)
	}
	s += ")"
	if st.Message != "" {
		s += ": " + st.Message
	}
	return s
}

func printNotified(out io.Writer, st maintenance.Status) {
	if st.Notified == nil || !*st.Notified {
		_, _ = fmt.Fprintln(out, "notified: false (email off or no recipients)")
		return
	}
	_, _ = fmt.Fprintln(out, "notified: true (queued; the running server sends it on its next email poll)")
	if st.Active && st.Mode == maintenance.Frozen {
		_, _ = fmt.Fprintln(out, "note: email is paused while frozen; it goes out once the window ends. Announce with a scheduled window before freezing.")
	}
}

func printMaintenanceState(out io.Writer, state usecase.MaintenanceState) {
	if state.Server == nil {
		_, _ = fmt.Fprintf(out, "server off (source %s)\n", state.Source)
	} else {
		_, _ = fmt.Fprintf(out, "%s [source %s]\n", describeStatus("server", *state.Server), state.Source)
	}
	for _, st := range state.Orgs {
		_, _ = fmt.Fprintln(out, describeStatus("organization "+st.Org, st))
	}
	for _, st := range state.Projects {
		_, _ = fmt.Fprintln(out, describeStatus("project "+st.Project, st))
	}
}
