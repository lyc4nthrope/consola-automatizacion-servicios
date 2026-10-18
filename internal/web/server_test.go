package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	assets "github.com/lyc4nthrope/consola-automatizacion-servicios/web"
)

func do(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	NewHandler(assets.Static).ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	rec := do(t, http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}
}

func TestHealthzRejectsOtherMethods(t *testing.T) {
	if rec := do(t, http.MethodPost, "/healthz"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestEmbeddedAssetsAreServed(t *testing.T) {
	cases := []struct {
		path        string
		contentType string
	}{
		{"/static/vendor/bootstrap/css/bootstrap.min.css", "text/css; charset=utf-8"},
		{"/static/vendor/bootstrap/js/bootstrap.bundle.min.js", "text/javascript; charset=utf-8"},
		{"/static/vendor/bootstrap-icons/bootstrap-icons.min.css", "text/css; charset=utf-8"},
		{"/static/vendor/bootstrap-icons/fonts/bootstrap-icons.woff2", "font/woff2"},
		{"/static/vendor/bootstrap-icons/fonts/bootstrap-icons.woff", "font/woff"},
	}
	for _, c := range cases {
		rec := do(t, http.MethodGet, c.path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", c.path, rec.Code)
			continue
		}
		// Exact match: the type must come from the console's own table, not from the host.
		if ct := rec.Header().Get("Content-Type"); ct != c.contentType {
			t.Errorf("%s: Content-Type = %q, want %q", c.path, ct, c.contentType)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing X-Content-Type-Options: nosniff", c.path)
		}
		if rec.Body.Len() < 1000 {
			t.Errorf("%s: body has %d bytes, expected the real asset", c.path, rec.Body.Len())
		}
	}
}

func TestUnknownStaticExtensionIsNotGuessed(t *testing.T) {
	rec := do(t, http.MethodGet, "/static/vendor/README.md")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", ct)
	}
}

func TestStaticDirectoriesAreNotListed(t *testing.T) {
	for _, p := range []string{"/static/", "/static/vendor/", "/static/vendor/bootstrap/css/"} {
		if rec := do(t, http.MethodGet, p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404 (no directory listing)", p, rec.Code)
		}
	}
}

func TestIndexReferencesNoExternalHost(t *testing.T) {
	rec := do(t, http.MethodGet, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	body, _ := io.ReadAll(rec.Body)
	page := string(body)
	if !strings.Contains(page, `href="/static/vendor/bootstrap/css/bootstrap.min.css"`) {
		t.Fatal("index must load the embedded Bootstrap stylesheet")
	}
	// The console must work offline: no URL pointing to another host anywhere in the page
	// (attributes with any quoting, srcset, inline CSS url() or @import).
	external := regexp.MustCompile(`(?i)(https?:)?//[a-z0-9-]+(\.[a-z0-9-]+)+`)
	if m := external.FindString(page); m != "" {
		t.Fatalf("index references an external host: %s", m)
	}
}

func TestUnknownRouteIsNotFound(t *testing.T) {
	if rec := do(t, http.MethodGet, "/no-existe"); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
