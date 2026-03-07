package viewer

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embeddedDist embed.FS

// NewStaticHandler serves the embedded SPA and falls back to index.html for routes.
func NewStaticHandler() (http.Handler, error) {
	distFS, err := fs.Sub(embeddedDist, "dist")
	if err != nil {
		return nil, err
	}

	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		requestPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if requestPath == "." || requestPath == "" {
			serveIndex(w, r, distFS)
			return
		}

		if _, err := fs.Stat(distFS, requestPath); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		serveIndex(w, r, distFS)
	}), nil
}

func serveIndex(w http.ResponseWriter, r *http.Request, distFS fs.FS) {
	data, err := fs.ReadFile(distFS, "index.html")
	if err != nil {
		http.Error(w, "viewer assets missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
