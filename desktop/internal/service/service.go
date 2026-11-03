// Package service is the API the desktop frontend calls through Wails
// bindings. It talks to the nipa daemon and keeps the list of opened clones.
package service

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/desktop/internal/connector"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type DaemonInfo struct {
	Version string `json:"version"`
	PID     int    `json:"pid"`
	Port    int    `json:"port"`
	Started bool   `json:"started"`
}

type Repo struct {
	Root   string   `json:"root"`
	Name   string   `json:"name"`
	URL    string   `json:"url"`
	Branch string   `json:"branch"`
	Sparse []string `json:"sparse"`
	// Error is set when the clone could not be watched (moved, deleted, not a
	// clone anymore); the app keeps showing it so the user can remove it.
	Error string `json:"error,omitempty"`
}

type RepoStore interface {
	Repos() ([]string, error)
	Add(root string) error
	Remove(root string) error
}

// Session is one daemon connection.
type Session interface {
	Client() daemonpb.NipaDaemonClient
	Info() DaemonInfo
	Close() error
}

type Dialer func(ctx context.Context) (Session, error)

// FolderPicker asks the user for a directory; an empty path means cancelled.
type FolderPicker func() (string, error)

// Emitter pushes an event to the frontend.
type Emitter func(name string, data any)

const EventRepoChanged = "repo:changed"

// RepoChanged tells the frontend a watched clone may have changed; no paths
// means refresh everything.
type RepoChanged struct {
	Root  string   `json:"root"`
	Paths []string `json:"paths"`
}

type Service struct {
	// Events receives change and progress events; nil drops them.
	Events Emitter

	dial  Dialer
	store RepoStore
	pick  FolderPicker

	mu     sync.Mutex
	active Session
	// watched holds the roots registered with the active session and the
	// cancel func of their change follower; it is reset with the session
	// because a restarted daemon forgets every watch.
	watched map[string]context.CancelFunc
}

func New(dial Dialer, store RepoStore, pick FolderPicker) *Service {
	return &Service{dial: dial, store: store, pick: pick, watched: map[string]context.CancelFunc{}}
}

func (s *Service) emit(name string, data any) {
	if s.Events != nil {
		s.Events(name, data)
	}
}

// ConnectorDialer dials the daemon through the connector package.
func ConnectorDialer(opts connector.Options) Dialer {
	return func(ctx context.Context) (Session, error) {
		conn, err := connector.Connect(ctx, opts)
		if err != nil {
			return nil, err
		}
		return connectorSession{conn}, nil
	}
}

type connectorSession struct{ conn *connector.Conn }

func (s connectorSession) Client() daemonpb.NipaDaemonClient { return s.conn.Client }
func (s connectorSession) Close() error                      { return s.conn.Close() }
func (s connectorSession) Info() DaemonInfo {
	return DaemonInfo{
		Version: s.conn.Version,
		PID:     s.conn.Endpoint.PID,
		Port:    s.conn.Endpoint.Port,
		Started: s.conn.Started,
	}
}

// Connect attaches to the daemon, starting it when needed.
func (s *Service) Connect(ctx context.Context) (DaemonInfo, error) {
	sess, err := s.session(ctx)
	if err != nil {
		return DaemonInfo{}, err
	}
	return sess.Info(), nil
}

// Repos returns the saved clones, watching each one with the daemon.
func (s *Service) Repos(ctx context.Context) ([]Repo, error) {
	roots, err := s.store.Repos()
	if err != nil {
		return nil, err
	}
	if len(roots) > 0 {
		if _, err := s.session(ctx); err != nil {
			return nil, err
		}
	}
	out := make([]Repo, 0, len(roots))
	for _, root := range roots {
		repo, err := s.watch(ctx, root)
		if err != nil {
			if isConnectionError(err) {
				return nil, cleanError(err)
			}
			repo = Repo{Root: root, Name: filepath.Base(root), Error: cleanError(err).Error()}
		}
		out = append(out, repo)
	}
	return out, nil
}

// PickRepo lets the user choose a folder inside a clone and adds it. A nil
// repo means the dialog was cancelled.
func (s *Service) PickRepo(ctx context.Context) (*Repo, error) {
	path, err := s.pick()
	if err != nil || path == "" {
		return nil, err
	}
	repo, err := s.AddRepo(ctx, path)
	if err != nil {
		return nil, err
	}
	return &repo, nil
}

// AddRepo watches the clone containing path and remembers its root.
func (s *Service) AddRepo(ctx context.Context, path string) (Repo, error) {
	repo, err := s.watch(ctx, path)
	if err != nil {
		return Repo{}, cleanError(err)
	}
	if err := s.store.Add(repo.Root); err != nil {
		return Repo{}, err
	}
	return repo, nil
}

// RemoveRepo forgets a clone; it is never deleted from disk.
func (s *Service) RemoveRepo(ctx context.Context, root string) error {
	s.mu.Lock()
	if cancel, ok := s.watched[root]; ok {
		cancel()
		delete(s.watched, root)
	}
	s.mu.Unlock()
	if sess, err := s.session(ctx); err == nil {
		_, err := sess.Client().UnwatchRepo(ctx, &daemonpb.UnwatchRepoRequest{Root: root})
		s.dropIfDisconnected(err)
	}
	return s.store.Remove(root)
}

func (s *Service) ServiceShutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return nil
	}
	return s.resetLocked()
}

func (s *Service) resetLocked() error {
	for _, cancel := range s.watched {
		cancel()
	}
	s.watched = map[string]context.CancelFunc{}
	err := s.active.Close()
	s.active = nil
	return err
}

func (s *Service) watch(ctx context.Context, path string) (Repo, error) {
	sess, err := s.session(ctx)
	if err != nil {
		return Repo{}, err
	}
	resp, err := sess.Client().WatchRepo(ctx, &daemonpb.WatchRepoRequest{Root: path})
	if err != nil {
		s.dropIfDisconnected(err)
		return Repo{}, err
	}
	info := resp.GetRepo()
	s.follow(sess, info.GetRoot())
	return Repo{
		Root:   info.GetRoot(),
		Name:   filepath.Base(info.GetRoot()),
		URL:    info.GetUrl(),
		Branch: info.GetBranch(),
		Sparse: info.GetSparse(),
	}, nil
}

func (s *Service) session(ctx context.Context) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		return s.active, nil
	}
	sess, err := s.dial(ctx)
	if err != nil {
		return nil, err
	}
	s.active = sess
	return sess, nil
}

// client returns a daemon client for which root is watched, re-watching it
// after a reconnect.
func (s *Service) client(ctx context.Context, root string) (daemonpb.NipaDaemonClient, error) {
	s.mu.Lock()
	sess := s.active
	_, watched := s.watched[root]
	s.mu.Unlock()
	if sess != nil && watched {
		return sess.Client(), nil
	}
	if _, err := s.watch(ctx, root); err != nil {
		return nil, cleanError(err)
	}
	sess, err := s.session(ctx)
	if err != nil {
		return nil, err
	}
	return sess.Client(), nil
}

// follow relays the daemon's change notices for root as frontend events
// until the stream ends or the session is reset.
func (s *Service) follow(sess Session, root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != sess {
		return
	}
	if _, ok := s.watched[root]; ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.watched[root] = cancel
	go func() {
		stream, err := sess.Client().WatchChanges(ctx, &daemonpb.WatchChangesRequest{Root: root})
		if err != nil {
			return
		}
		for {
			change, err := stream.Recv()
			if err != nil {
				return
			}
			s.emit(EventRepoChanged, RepoChanged{Root: root, Paths: change.GetPaths()})
		}
	}()
}

// dropIfDisconnected forgets a session whose daemon went away, so the next
// call reconnects (and restarts the daemon when needed).
func (s *Service) dropIfDisconnected(err error) {
	if !isConnectionError(err) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		_ = s.resetLocked()
	}
}

func isConnectionError(err error) bool {
	return status.Code(err) == codes.Unavailable
}

// cleanError strips the gRPC envelope so the UI shows the daemon's message.
func cleanError(err error) error {
	if st, ok := status.FromError(err); ok && st.Code() != codes.OK {
		return errors.New(st.Message())
	}
	return err
}
