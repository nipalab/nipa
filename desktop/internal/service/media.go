package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// MediaPrefix is the asset-server path the webview loads media files from.
const MediaPrefix = "/nipa-media/"

const maxMediaFiles = 8

var mediaTypes = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mov": "video/quicktime", ".webm": "video/webm",
	".mkv": "video/x-matroska", ".avi": "video/x-msvideo",
}

type mediaFile struct {
	path string
	name string
}

// mediaStore keeps the most recently used media files written for the
// viewers in a private temp folder, so the webview can stream them with
// range requests instead of holding them as data URLs. The video viewer does
// not cache its answer beyond its own lifetime, so mounted viewers (a few at
// most) never hold an evicted token.
type mediaStore struct {
	mu    sync.Mutex
	dir   string
	files map[string]mediaFile
	order []string
}

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// create opens a new temp file for name; commit publishes it and returns
// its token, discard removes it.
func (m *mediaStore) create(name string) (*os.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dir == "" {
		dir, err := os.MkdirTemp("", "nipa-media-*")
		if err != nil {
			return nil, err
		}
		m.dir, m.files = dir, map[string]mediaFile{}
	}
	return os.CreateTemp(m.dir, "media-*"+strings.ToLower(path.Ext(name)))
}

func (m *mediaStore) commit(f *os.File, name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	token := newToken()
	m.files[token] = mediaFile{path: f.Name(), name: path.Base(name)}
	m.order = append(m.order, token)
	for len(m.order) > maxMediaFiles {
		old := m.order[0]
		m.order = m.order[1:]
		_ = os.Remove(m.files[old].path)
		delete(m.files, old)
	}
	return token
}

// lookup finds a file and marks it recently used, so a video being streamed
// is evicted last.
func (m *mediaStore) lookup(token string) (mediaFile, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[token]
	if ok {
		i := slices.Index(m.order, token)
		m.order = append(append(m.order[:i:i], m.order[i+1:]...), token)
	}
	return f, ok
}

func (m *mediaStore) close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dir == "" {
		return nil
	}
	err := os.RemoveAll(m.dir)
	m.dir, m.files, m.order = "", nil, nil
	return err
}

func mediaURL(token, name string) string {
	return MediaPrefix + token + "/" + path.Base(name)
}

// MediaMiddleware serves the files the video viewer prepared under
// MediaPrefix (with range requests) and passes every other request on.
func MediaMiddleware(s *Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rest, ok := strings.CutPrefix(r.URL.Path, MediaPrefix)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			token, _, _ := strings.Cut(rest, "/")
			file, found := s.media.lookup(token)
			if !found || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
				http.NotFound(w, r)
				return
			}
			f, err := os.Open(file.path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer f.Close()
			st, err := f.Stat()
			if err != nil {
				http.NotFound(w, r)
				return
			}
			if ct := mediaTypes[strings.ToLower(filepath.Ext(file.name))]; ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			w.Header().Set("Cache-Control", "no-store")
			http.ServeContent(w, r, file.name, st.ModTime(), f)
		})
	}
}

// HasMedia reports whether a prepared media file is still available; the
// viewer asks after a playback error to tell an evicted file from a format
// the webview cannot decode.
func (s *Service) HasMedia(token string) bool {
	_, ok := s.media.lookup(token)
	return ok
}

// OpenMedia opens a file the viewer prepared in the system's default
// application.
func (s *Service) OpenMedia(token string) error {
	file, ok := s.media.lookup(token)
	if !ok {
		return errors.New("this preview is no longer available, reload the file")
	}
	if s.OpenFunc == nil {
		return errors.New("opening files is not available")
	}
	return s.OpenFunc(file.path)
}
