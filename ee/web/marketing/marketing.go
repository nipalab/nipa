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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == BasePath {
			http.Redirect(w, r, BasePath+"/", http.StatusMovedPermanently)
			return
		}
		rel := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(r.URL.Path, BasePath)), "/")
		if rel == "" {
			writeHTMLPage(w, indexHTML)
			return
		}
		file, ok := lookup(sub, rel)
		if !ok {
			writeHTMLPage(w, indexHTML)
			return
		}
		// http.FileServer redirects any path ending in /index.html, so page
		// files are written directly; assets still go through the file server.
		if strings.HasSuffix(file, "index.html") {
			data, err := fs.ReadFile(sub, file)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			writeHTMLPage(w, data)
			return
		}
		serveFile(w, r, fileServer, file)
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

// lookup resolves a request path to a concrete file, expanding directory
// paths to their index.html so the file server never issues redirects.
func lookup(sub fs.FS, rel string) (string, bool) {
	info, err := fs.Stat(sub, rel)
	if err != nil {
		if file := rel + ".html"; exists(sub, file) {
			return file, true
		}
		return "", false
	}
	if info.IsDir() {
		index := path.Join(rel, "index.html")
		if exists(sub, index) {
			return index, true
		}
		return "", false
	}
	return rel, true
}

func exists(sub fs.FS, name string) bool {
	info, err := fs.Stat(sub, name)
	return err == nil && !info.IsDir()
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
