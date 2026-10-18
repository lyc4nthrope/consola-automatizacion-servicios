// Package web is the HTTP adapter of the console: routes, handlers and static files.
package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// NewHandler returns the console routes. static must contain the "static" directory.
func NewHandler(static fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", index)
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("GET /static/", staticFiles(http.FileServerFS(static)))
	return mux
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// staticTypes fixes the Content-Type of the embedded assets. Go's mime package does not
// know font types and falls back to the host's /etc/mime.types, which differs between
// machines (and does not exist on Windows), so the console declares them itself.
var staticTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".svg":   "image/svg+xml",
	".png":   "image/png",
}

// staticFiles serves only files (no directory listings) with a fixed Content-Type.
func staticFiles(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		ct, ok := staticTypes[path.Ext(r.URL.Path)]
		if !ok {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// index is a temporary page for F0; F1 replaces it with the module home built from templates.
func index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

const indexHTML = `<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Consola de automatización de servicios</title>
<link rel="stylesheet" href="/static/vendor/bootstrap/css/bootstrap.min.css">
<link rel="stylesheet" href="/static/vendor/bootstrap-icons/bootstrap-icons.min.css">
</head>
<body class="bg-body-tertiary">
<main class="container py-5">
<h1 class="h3"><i class="bi bi-cloud-fill" aria-hidden="true"></i> Consola de automatización de servicios</h1>
<p class="text-body-secondary">Computación en la Nube 2026-2 · Base del proyecto en construcción.</p>
</main>
</body>
</html>
`
