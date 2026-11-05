package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"

	"github.com/nipalab/nipa/desktop/internal/service"
	"github.com/nipalab/nipa/desktop/internal/update"
)

const (
	firstUpdateCheck = time.Minute
	updateCheckEvery = 6 * time.Hour
)

func setupUpdates(app *application.App, svc *service.Service, current string) {
	cfg := service.Updates{Current: current, OpenURL: app.Browser.OpenURL}
	defer func() { service.ConfigureUpdates(svc, cfg) }()
	if current == "" {
		return
	}
	key, err := update.Key()
	if err != nil {
		log.Printf("updates disabled: %v", err)
		return
	}
	provider, err := endpoint.New(endpoint.Config{URL: update.Feed()})
	if err != nil {
		log.Printf("updates disabled: %v", err)
		return
	}
	if err := app.Updater.Init(updater.Config{
		CurrentVersion: current,
		Providers:      []updater.Provider{provider},
		PublicKey:      key,
		Window:         updater.WindowNone,
	}); err != nil {
		log.Printf("updates disabled: %v", err)
		return
	}
	if exe, err := os.Executable(); err == nil {
		cfg.InPlace = update.Replaceable(update.Target(exe))
	}
	cfg.Updater = wailsUpdater{app: app}
	ctx, cancel := context.WithCancel(context.Background())
	app.OnShutdown(cancel)
	go service.WatchUpdates(ctx, svc, firstUpdateCheck, updateCheckEvery)
}

type wailsUpdater struct{ app *application.App }

func (w wailsUpdater) Check(ctx context.Context) (*service.Release, error) {
	rel, err := w.app.Updater.Check(ctx)
	if err != nil || rel == nil {
		return nil, err
	}
	out := &service.Release{Version: rel.Version, Notes: rel.Notes}
	if !rel.PublishedAt.IsZero() {
		out.PublishedAt = rel.PublishedAt.UTC().Format(time.RFC3339)
	}
	out.Page, _ = rel.Metadata["page"].(string)
	return out, nil
}

func (w wailsUpdater) Download(ctx context.Context, progress func(written, total int64)) error {
	off := w.app.Event.On(updater.EventDownloadProgress, func(e *application.CustomEvent) {
		if p, ok := progressOf(e.Data); ok {
			progress(p.Written, p.Total)
		}
	})
	defer off()
	return w.app.Updater.DownloadAndInstall(ctx)
}

func (w wailsUpdater) Restart(ctx context.Context) error {
	return w.app.Updater.Restart(ctx)
}

func progressOf(data any) (updater.Progress, bool) {
	switch p := data.(type) {
	case updater.Progress:
		return p, true
	case *updater.Progress:
		if p != nil {
			return *p, true
		}
	case []any:
		if len(p) == 1 {
			return progressOf(p[0])
		}
	}
	return updater.Progress{}, false
}
