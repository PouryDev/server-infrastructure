package httpapi

import (
	"net/http"
	"path"
	"strings"
)

// uiCSP replaces httpx's API-only policy (default-src 'none') on the pages of the UI. Scripts and styles are the
// build's own files; React sets inline style attributes, hence 'unsafe-inline' for styles only.
const uiCSP = "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; " +
	"connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// spa serves a single-page app: a file of dir when there is one, index.html otherwise (client-side routes).
// Unknown API paths are not rewritten to index.html: they answer 404 like any API.
func spa(dir string) http.Handler {
	files := http.Dir(dir) // confines every lookup to dir
	serve := func(w http.ResponseWriter, r *http.Request, name string) bool {
		f, err := files.Open(name)
		if err != nil {
			return false
		}
		defer func() { _ = f.Close() }()
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			return false
		}
		http.ServeContent(w, r, st.Name(), st.ModTime(), f)
		return true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean("/" + r.URL.Path)
		if p == "/v1" || p == "/auth" || strings.HasPrefix(p, "/v1/") || strings.HasPrefix(p, "/auth/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", uiCSP)
		if p != "/" {
			if strings.HasPrefix(p, "/assets/") { // content-hashed by the build
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			if serve(w, r, p) {
				return
			}
			w.Header().Del("Cache-Control")
		}
		w.Header().Set("Cache-Control", "no-cache")
		if !serve(w, r, "/index.html") {
			http.NotFound(w, r)
		}
	})
}
