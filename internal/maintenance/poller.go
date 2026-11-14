package maintenance

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/nipalab/nipa/internal/dispatch"
)

const DefaultPollInterval = 5 * time.Second

type LoadFunc func(ctx context.Context) ([]Window, error)

// Poller reloads the stored windows and the override file on an interval and
// publishes them to the Service. A failed load keeps the stored windows of the
// current snapshot.
type Poller struct {
	svc      *Service
	load     LoadFunc
	file     string
	interval time.Duration
	clock    func() time.Time
	onError  func(error)
	runner   *dispatch.Runner
}

func NewPoller(svc *Service, load LoadFunc, file string, interval time.Duration) *Poller {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	return &Poller{
		svc:      svc,
		load:     load,
		file:     file,
		interval: interval,
		clock:    time.Now,
		runner:   dispatch.NewRunner(),
	}
}

func (p *Poller) WithClock(clock func() time.Time) *Poller {
	p.clock = clock
	return p
}

// WithErrorHook is called for every failed poll, e.g. to count it.
func (p *Poller) WithErrorHook(fn func(error)) *Poller {
	p.onError = fn
	return p
}

func (p *Poller) Start() {
	p.runner.Start(0, nil, p.loop)
}

func (p *Poller) Stop(ctx context.Context) error {
	return p.runner.Stop(ctx)
}

func (p *Poller) loop() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-p.runner.StopCh()
		cancel()
	}()
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		p.pollAndLog(ctx)
		select {
		case <-p.runner.StopCh():
			return
		case <-ticker.C:
		}
	}
}

func (p *Poller) pollAndLog(ctx context.Context) {
	if err := p.Poll(ctx); err != nil && ctx.Err() == nil {
		slog.Warn("maintenance poll failed", "error", err)
		if p.onError != nil {
			p.onError(err)
		}
	}
}

// Poll runs one reload and publishes the result.
func (p *Poller) Poll(ctx context.Context) error {
	var errs []error
	windows, err := p.load(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("load maintenance windows: %w", err))
		windows = p.svc.Snapshot().Windows()
	}
	snap := NewSnapshot(windows, p.clock())
	if err := p.applyFile(snap); err != nil {
		errs = append(errs, err)
	}
	if err := p.svc.Apply(ctx, snap); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (p *Poller) applyFile(snap *Snapshot) error {
	if p.file == "" {
		return nil
	}
	data, err := os.ReadFile(p.file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		snap.Server, snap.Source, snap.FileError = &Window{Mode: Full}, SourceFile, err.Error()
		return fmt.Errorf("read maintenance file: %w", err)
	}
	w, err := ParseFile(data)
	if err != nil {
		snap.Server, snap.Source, snap.FileError = &Window{Mode: Full}, SourceFile, err.Error()
		return err
	}
	snap.Server, snap.Source = &w, SourceFile
	return nil
}
