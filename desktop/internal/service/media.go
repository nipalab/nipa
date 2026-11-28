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

// A file stays while a viewer holds it. Released files linger a little for
// an external player that may still read them; maxHeldMedia only bounds
// files whose viewer never released them (a request that outlived its view).
// A model with its textures holds one file per referenced file, so the held
// cap leaves room for a few of them.
const (
	maxReleasedMedia = 4
	maxHeldMedia     = 160
)

var mediaTypes = map[string]string{
	// video
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mov": "video/quicktime", ".webm": "video/webm",
	".mkv": "video/x-matroska", ".avi": "video/x-msvideo",
	// audio playback copies
	".wav": "audio/wav",
	// models the viewer loads and the files they reference
	".glb": "model/gltf-binary", ".gltf": "model/gltf+json", ".obj": "model/obj", ".mtl": "model/mtl",
	".fbx": "application/octet-stream", ".stl": "model/stl", ".ply": "application/octet-stream",
	".usd": "model/vnd.usda", ".usda": "model/vnd.usda", ".usdc": "model/vnd.usdc", ".usdz": "model/vnd.usdz+zip",
	".bin": "application/octet-stream",
	// textures
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif",
	".bmp": "image/bmp", ".tga": "image/x-tga", ".ktx2": "image/ktx2", ".dds": "image/vnd-ms.dds",
}

// mediaType is the Content-Type the media route serves a file name with.
func mediaType(name string) string {
	if ct := mediaTypes[strings.ToLower(path.Ext(name))]; ct != "" {
		return ct
	}
	return "application/octet-stream"
}

type mediaFile struct {
	path string
	name string
	held bool
	// owned files were written by the store and are removed with their
	// token; a working-copy file registered in place never is.
	owned bool
}

// mediaStore keeps the files the viewers stream (videos, models and their
// textures, audio playback copies) so the webview loads them over the asset
// server with range requests instead of base64 inside a bound call's JSON.
// Stored versions are written to a private temp folder; working-copy files
// are registered where they are and never deleted by the store. A file is
// held from the moment its viewer call returns the token until the viewer
// releases it, and only released files are evicted, oldest first.
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
		m.dir = dir
	}
	return os.CreateTemp(m.dir, "media-*"+strings.ToLower(path.Ext(name)))
}

func (m *mediaStore) commit(f *os.File, name string) string {
	return m.add(f.Name(), name, true)
}

func (m *mediaStore) add(p, name string, owned bool) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.files == nil {
		m.files = map[string]mediaFile{}
	}
	token := newToken()
	m.files[token] = mediaFile{path: p, name: path.Base(name), held: true, owned: owned}
	m.order = append(m.order, token)
	m.evictLocked()
	return token
}

// write stores data as a new media file named name.
func (m *mediaStore) write(name string, data []byte) (string, error) {
	f, err := m.create(name)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return m.commit(f, name), nil
}

// register publishes a working-copy file without copying it. full must come
// from resolveWorkingFile; the store never deletes it.
func (m *mediaStore) register(full, name string) string {
	return m.add(full, name, false)
}

// drop forgets tokens at once, removing the files the store wrote; for a
// viewer call that failed after publishing some of its files.
func (m *mediaStore) drop(tokens ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range tokens {
		if i := slices.Index(m.order, t); i >= 0 {
			m.order = slices.Delete(m.order, i, i+1)
		}
		m.removeLocked(t)
	}
}

func (m *mediaStore) removeLocked(token string) {
	f, ok := m.files[token]
	if !ok {
		return
	}
	if f.owned {
		_ = os.Remove(f.path)
	}
	delete(m.files, token)
}

// lookup finds a file and marks it recently used; hold also marks it held
// again, for a viewer that streams a file it released (a remount).
func (m *mediaStore) lookup(token string, hold bool) (mediaFile, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[token]
	if ok {
		i := slices.Index(m.order, token)
		m.order = append(append(m.order[:i:i], m.order[i+1:]...), token)
		if hold && !f.held {
			f.held = true
			m.files[token] = f
		}
	}
	return f, ok
}

// release lets the store evict the files of a viewer that closed.
func (m *mediaStore) release(tokens []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range tokens {
		if f, ok := m.files[t]; ok {
			f.held = false
			m.files[t] = f
		}
	}
	m.evictLocked()
}

func (m *mediaStore) evictLocked() {
	count := func(held bool) int {
		n := 0
		for _, f := range m.files {
			if f.held == held {
				n++
			}
		}
		return n
	}
	for _, held := range []bool{false, true} {
		limit := maxReleasedMedia
		if held {
			limit = maxHeldMedia
		}
		for over := count(held) - limit; over > 0; over-- {
			i := slices.IndexFunc(m.order, func(t string) bool { return m.files[t].held == held })
			token := m.order[i]
			m.order = slices.Delete(m.order, i, i+1)
			m.removeLocked(token)
		}
	}
}

// close removes every file the store wrote; registered working files stay.
func (m *mediaStore) close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	if m.dir != "" {
		err = os.RemoveAll(m.dir)
	}
	m.dir, m.files, m.order = "", nil, nil
	return err
}

var errNotWorkingFile = errors.New("not a working-copy file")

// resolveWorkingFile maps a repository-relative path of the working copy
// to the real file the media route may serve in place. It refuses paths that
// leave the clone (also through a symlink), anything under .nipa/ and
// anything that is not a regular file, so a token can never expose a file
// outside the repository.
func resolveWorkingFile(root, rel string) (string, int64, error) {
	p := filepath.ToSlash(strings.TrimSpace(rel))
	if root == "" || p == "" || path.IsAbs(p) || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", 0, errNotWorkingFile
	}
	p = path.Clean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, "../") || isMetadata(p) {
		return "", 0, errNotWorkingFile
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", 0, errNotWorkingFile
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", 0, errNotWorkingFile
	}
	full, err := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.FromSlash(p)))
	if err != nil {
		return "", 0, errNotWorkingFile
	}
	inside, err := filepath.Rel(realRoot, full)
	if err != nil || inside == "." || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
		return "", 0, errNotWorkingFile
	}
	if isMetadata(filepath.ToSlash(inside)) {
		return "", 0, errNotWorkingFile
	}
	st, err := os.Stat(full)
	if err != nil || !st.Mode().IsRegular() {
		return "", 0, errNotWorkingFile
	}
	return full, st.Size(), nil
}

func isMetadata(p string) bool {
	p = strings.ToLower(p)
	return p == ".nipa" || strings.HasPrefix(p, ".nipa/")
}

func isWorking(ref *ImageRef) bool { return ref.Source == "" || ref.Source == "working" }

func mediaURL(token, name string) string {
	return MediaPrefix + token + "/" + path.Base(name)
}

// MediaMiddleware serves the files the viewers prepared under MediaPrefix
// (with range requests) and passes every other request on.
func MediaMiddleware(s *Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rest, ok := strings.CutPrefix(r.URL.Path, MediaPrefix)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			token, _, _ := strings.Cut(rest, "/")
			file, found := s.media.lookup(token, true)
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
			if err != nil || !st.Mode().IsRegular() {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", mediaType(file.name))
			w.Header().Set("Cache-Control", "no-store")
			http.ServeContent(w, r, file.name, st.ModTime(), f)
		})
	}
}

// HasMedia reports whether a prepared media file is still available; the
// viewer asks after a load error to tell an evicted file from a format the
// webview cannot decode.
func (s *Service) HasMedia(token string) bool {
	_, ok := s.media.lookup(token, false)
	return ok
}

// ReleaseMedia tells the store a viewer no longer shows these files, so they
// may be removed.
func (s *Service) ReleaseMedia(tokens []string) {
	s.media.release(tokens)
}

// OpenMedia opens a file the viewer prepared in the system's default
// application.
func (s *Service) OpenMedia(token string) error {
	file, ok := s.media.lookup(token, false)
	if !ok {
		return errors.New("this preview is no longer available, reload the file")
	}
	if s.OpenFunc == nil {
		return errors.New("opening files is not available")
	}
	return s.OpenFunc(file.path)
}
