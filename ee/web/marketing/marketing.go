// Package marketing serves the embedded SaaS marketing site. The site is
// built under /web; the bare domain root answers with the site's index page.
package marketing

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// BasePath is the URL prefix the marketing site is mounted on.
const BasePath = "/web"

//go:embed all:dist
var dist embed.FS

func Handler() http.Handler {
	sub, indexHTML, err := embeddedDist()
	if err != nil {
		panic("marketing: " + err.Error())
	}
	return newHandler(sub, indexHTML)
}

func newHandler(sub fs.FS, indexHTML []byte) http.Handler {
	fileServer := http.FileServer(http.FS(sub))
	pages, files := loadSite(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == BasePath {
			http.Redirect(w, r, BasePath+"/", http.StatusMovedPermanently)
			return
		}
		rel := pagePath(r.URL.Path)
		if rel == "" {
			writeHTMLPage(w, indexHTML)
			return
		}
		// Pages come from the preloaded map: http.FileServer redirects any
		// path ending in /index.html, so they are written directly.
		if page, ok := pages[rel]; ok {
			writeHTMLPage(w, page)
			return
		}
		if files[rel] {
			serveFile(w, r, fileServer, rel)
			return
		}
		writeHTMLPage(w, indexHTML)
	})
}

func embeddedDist() (fs.FS, []byte, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, nil, fmt.Errorf("dist is missing: %w", err)
	}
	indexHTML, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, nil, fmt.Errorf("no index.html in dist: %w", err)
	}
	return sub, indexHTML, nil
}

// pagePath normalizes a request path to a path relative to the embedded site.
func pagePath(urlPath string) string {
	return strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(urlPath, BasePath)), "/")
}

// loadSite indexes the embedded site once so request paths are only looked up
// in memory, never handed to a filesystem read. Directory routes resolve to
// their index.html; extensionless routes also try a sibling .html file.
func loadSite(sub fs.FS) (pages map[string][]byte, files map[string]bool) {
	files = map[string]bool{}
	var htmlFiles []string
	_ = fs.WalkDir(sub, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		files[name] = true
		if strings.HasSuffix(name, ".html") {
			htmlFiles = append(htmlFiles, name)
		}
		return nil
	})

	pages = make(map[string][]byte, len(htmlFiles)*2)
	contents := make(map[string][]byte, len(htmlFiles))
	for _, name := range htmlFiles {
		data, err := fs.ReadFile(sub, name)
		if err != nil {
			continue
		}
		contents[name] = data
		pages[name] = data
		if path.Base(name) != "index.html" {
			pages[strings.TrimSuffix(name, ".html")] = data
		}
	}
	// Directory indexes win over a sibling page with the same route, matching
	// the file server's directory-first lookup for an existing directory.
	for _, name := range htmlFiles {
		if path.Base(name) != "index.html" {
			continue
		}
		if dir := path.Dir(name); dir != "." {
			pages[dir] = contents[name]
		}
	}
	return pages, files
}

func serveFile(w http.ResponseWriter, r *http.Request, fileServer http.Handler, file string) {
	clone := r.Clone(r.Context())
	clone.URL.Path = "/" + file
	clone.URL.RawPath = ""
	fileServer.ServeHTTP(w, clone)
}

func writeHTMLPage(w http.ResponseWriter, html []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(html)
}
