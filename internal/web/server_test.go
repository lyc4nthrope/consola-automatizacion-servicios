package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	assets "github.com/lyc4nthrope/consola-automatizacion-servicios/web"
)

func newTestState() *app.State {
	return app.NewState(app.Config{Listen: app.DefaultListen, Targets: []app.Target{
		{Name: "pruebas", VM: "consola-pruebas", IP: "192.168.40.60", SSHUser: "root"},
		{Name: "balanceador", VM: "balanceador", IP: "192.168.40.53", SSHUser: "root"},
	}})
}

func newTestHandler(t *testing.T, state *app.State) http.Handler {
	t.Helper()
	h, err := NewHandler(assets.Static, assets.Templates, state, testListen)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

const testListen = "127.0.0.1:8090"

func serve(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	if req.Host == "example.com" { // httptest default: address the console like a browser would
		req.Host = testListen
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func do(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, newTestHandler(t, newTestState()), httptest.NewRequest(method, path, nil))
}

func postForm(path string, form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func body(rec *httptest.ResponseRecorder) string {
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}

var pages = []struct {
	path string
	h1   string
}{
	{"/", "Panel"},
	{"/preparacion", "Preparación del sistema"},
	{"/usuarios", "Gestión de usuarios"},
	{"/servicios", "Servicios systemd"},
	{"/balanceador", "Balanceador HAProxy"},
}

func TestHealthz(t *testing.T) {
	rec := do(t, http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if b := strings.TrimSpace(body(rec)); b != "ok" {
		t.Fatalf("body = %q, want ok", b)
	}
}

func TestHealthzRejectsOtherMethods(t *testing.T) {
	if rec := do(t, http.MethodPost, "/healthz"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestEveryPageRendersWithItsTitle(t *testing.T) {
	for _, p := range pages {
		rec := do(t, http.MethodGet, p.path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", p.path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type = %q", p.path, ct)
		}
		page := body(rec)
		h1 := regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`).FindAllStringSubmatch(page, -1)
		if len(h1) != 1 {
			t.Errorf("%s: want exactly one <h1>, got %d", p.path, len(h1))
			continue
		}
		if got := strings.TrimSpace(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(h1[0][1], "")); got != p.h1 {
			t.Errorf("%s: <h1> = %q, want %q", p.path, got, p.h1)
		}
	}
}

func TestNavigationMarksOnlyTheCurrentPage(t *testing.T) {
	for _, p := range pages {
		page := body(do(t, http.MethodGet, p.path))
		current := regexp.MustCompile(`<a [^>]*aria-current="page"[^>]*>`).FindAllString(page, -1)
		if len(current) != 1 {
			t.Errorf("%s: want exactly one link with aria-current=page, got %d", p.path, len(current))
			continue
		}
		if !strings.Contains(current[0], `href="`+p.path+`"`) {
			t.Errorf("%s: aria-current is on %s", p.path, current[0])
		}
	}
}

func TestLayoutEssentials(t *testing.T) {
	page := body(do(t, http.MethodGet, "/"))
	for _, want := range []string{
		`<html lang="es" data-bs-theme="dark">`,
		`href="#contenido"`, // skip link
		`id="contenido"`,
		`aria-label="Cambiar a tema claro"`,
		`aria-label="Abrir menú"`,
		"Cristhian Osorio", "Daniel Perez", "Wiliam Carmona",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("layout is missing %q", want)
		}
	}
}

func TestTopbarShowsCurrentTargetAndChannel(t *testing.T) {
	state := newTestState()
	h := newTestHandler(t, state)
	// A module page: unlike the panel, only the topbar shows the target there.
	page := body(serve(t, h, httptest.NewRequest(http.MethodGet, "/servicios", nil)))
	for _, want := range []string{"pruebas", "192.168.40.60", "Sin conexión"} {
		if !strings.Contains(page, want) {
			t.Errorf("topbar is missing %q", want)
		}
	}
	_ = state.SetExecutor("pruebas", &executor.Fake{NameValue: "Guest Additions"})
	page = body(serve(t, h, httptest.NewRequest(http.MethodGet, "/servicios", nil)))
	if !strings.Contains(page, "Guest Additions") {
		t.Error("topbar must show the active channel after SetExecutor")
	}
}

func TestSelectTargetRedirectsBack(t *testing.T) {
	state := newTestState()
	h := newTestHandler(t, state)
	rec := serve(t, h, postForm("/destino", url.Values{"destino": {"balanceador"}, "volver": {"/servicios"}}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/servicios" {
		t.Fatalf("Location = %q, want /servicios", loc)
	}
	if got := state.Current().Name; got != "balanceador" {
		t.Fatalf("current target = %q, want balanceador", got)
	}
}

func TestSelectTargetRejectsUnknownTarget(t *testing.T) {
	state := newTestState()
	rec := serve(t, newTestHandler(t, state), postForm("/destino", url.Values{"destino": {"otra"}, "volver": {"/"}}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := state.Current().Name; got != "pruebas" {
		t.Fatalf("current target changed to %q", got)
	}
}

func TestSelectTargetNeverRedirectsOutside(t *testing.T) {
	for _, back := range []string{"//evil.example", "https://evil.example", "/\\evil.example", "javascript:alert(1)", "/no-existe", ""} {
		rec := serve(t, newTestHandler(t, newTestState()), postForm("/destino", url.Values{"destino": {"pruebas"}, "volver": {back}}))
		if loc := rec.Header().Get("Location"); loc != "/" {
			t.Errorf("volver=%q: Location = %q, want /", back, loc)
		}
	}
}

func TestCrossSiteFormsAreRejected(t *testing.T) {
	cases := []struct {
		name   string
		header map[string]string
		want   int
	}{
		{"cross-site fetch metadata", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-site but other origin", map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"foreign Origin", map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden},
		{"opaque Origin null", map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"same origin", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://" + testListen}, http.StatusSeeOther},
		{"no browser headers (curl)", nil, http.StatusSeeOther},
	}
	for _, c := range cases {
		state := newTestState()
		req := postForm("/destino", url.Values{"destino": {"balanceador"}, "volver": {"/"}})
		for k, v := range c.header {
			req.Header.Set(k, v)
		}
		rec := serve(t, newTestHandler(t, state), req)
		if rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d", c.name, rec.Code, c.want)
		}
		if c.want == http.StatusForbidden && state.Current().Name != "pruebas" {
			t.Errorf("%s: a rejected request changed the state", c.name)
		}
	}
}

func TestSecurityHeadersOnPages(t *testing.T) {
	rec := do(t, http.MethodGet, "/")
	want := map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "same-origin",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, d := range []string{"default-src 'self'", "frame-ancestors 'none'", "form-action 'self'"} {
		if !strings.Contains(csp, d) {
			t.Errorf("Content-Security-Policy %q is missing %q", csp, d)
		}
	}
}

func TestConfigValuesAreHTMLEscaped(t *testing.T) {
	// Every field shown from config.json, in text and attribute context (<option value>).
	const payload = `"><script>alert(1)</script>`
	state := app.NewState(app.Config{Listen: app.DefaultListen, Targets: []app.Target{
		{Name: payload + "n", VM: payload + "v", IP: payload + "i", SSHUser: payload + "u"},
	}})
	page := body(serve(t, newTestHandler(t, state), httptest.NewRequest(http.MethodGet, "/", nil)))
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Fatal("a value from config.json was rendered without escaping")
	}
	for _, suffix := range []string{"n", "v", "i", "u"} {
		if !strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;"+suffix) {
			t.Errorf("escaped form of field %q not found: it may not be rendered at all", suffix)
		}
	}
	if !strings.Contains(page, `value="&#34;&gt;&lt;script&gt;`) {
		t.Error("the target name in <option value> is not attribute-escaped")
	}
}

func TestRequestsForOtherHostsAreRefused(t *testing.T) {
	// DNS rebinding: evil.example resolves to 127.0.0.1, so the browser sends same-origin headers.
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		state := newTestState()
		req := postForm("/destino", url.Values{"destino": {"balanceador"}, "volver": {"/"}})
		req.Method = method
		req.Host = "evil.example:8090"
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Origin", "http://evil.example:8090")
		rec := serve(t, newTestHandler(t, state), req)
		if rec.Code != http.StatusMisdirectedRequest {
			t.Errorf("%s with foreign Host: status = %d, want 421", method, rec.Code)
		}
		if state.Current().Name != "pruebas" {
			t.Errorf("%s with foreign Host changed the state", method)
		}
	}
	for _, host := range []string{"127.0.0.1:8090", "localhost:8090"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		if rec := serve(t, newTestHandler(t, newTestState()), req); rec.Code != http.StatusOK {
			t.Errorf("Host %s: status = %d, want 200", host, rec.Code)
		}
	}
	for _, host := range []string{"127.0.0.1:9999", "localhost", "127.0.0.1", ""} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		if rec := serve(t, newTestHandler(t, newTestState()), req); rec.Code != http.StatusMisdirectedRequest {
			t.Errorf("Host %s: status = %d, want 421", host, rec.Code)
		}
	}
}

func TestNewHandlerRejectsInvalidListen(t *testing.T) {
	if _, err := NewHandler(assets.Static, assets.Templates, newTestState(), "127.0.0.1"); err == nil {
		t.Fatal("expected an error for a listen address without port")
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
		{"/static/css/consola.css", "text/css; charset=utf-8"},
		{"/static/js/tema.js", "text/javascript; charset=utf-8"},
		{"/static/js/consola.js", "text/javascript; charset=utf-8"},
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
		if rec.Body.Len() < 200 {
			t.Errorf("%s: body has %d bytes, expected the real asset", c.path, rec.Body.Len())
		}
	}
}

func TestEveryLinkedAssetExists(t *testing.T) {
	h := newTestHandler(t, newTestState())
	page := body(serve(t, h, httptest.NewRequest(http.MethodGet, "/", nil)))
	refs := regexp.MustCompile(`(?:href|src)="(/static/[^"]+)"`).FindAllStringSubmatch(page, -1)
	if len(refs) < 5 {
		t.Fatalf("expected the layout to reference at least 5 static assets, got %d", len(refs))
	}
	for _, r := range refs {
		if rec := serve(t, h, httptest.NewRequest(http.MethodGet, r[1], nil)); rec.Code != http.StatusOK {
			t.Errorf("layout links %s but it returns %d", r[1], rec.Code)
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

func TestPagesAndOwnAssetsReferenceNoExternalHost(t *testing.T) {
	// The console must work offline: no URL pointing to another host, dotted or not, in any
	// page or in the console's own CSS and JS (vendor files are checked by hash instead).
	external := regexp.MustCompile(`(?i)(https?:)?//[a-z0-9][a-z0-9.-]*`)
	paths := []string{"/no-existe", "/static/css/consola.css", "/static/js/tema.js", "/static/js/consola.js"}
	for _, p := range pages {
		paths = append(paths, p.path)
	}
	for _, p := range paths {
		if m := external.FindString(body(do(t, http.MethodGet, p))); m != "" {
			t.Errorf("%s references an external host: %s", p, m)
		}
	}
}

func TestUnknownRouteShowsNotFoundPage(t *testing.T) {
	rec := do(t, http.MethodGet, "/no-existe")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	page := body(rec)
	if !strings.Contains(page, "Página no encontrada") || !strings.Contains(page, `href="/"`) {
		t.Fatal("404 page must explain the problem and link back to the panel")
	}
}

func TestSelectTargetRejectsOversizedForms(t *testing.T) {
	state := newTestState()
	form := url.Values{"destino": {"balanceador"}, "volver": {"/"}, "relleno": {strings.Repeat("x", 70_000)}}
	rec := serve(t, newTestHandler(t, state), postForm("/destino", form))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a body over the limit", rec.Code)
	}
	if got := state.Current().Name; got != "pruebas" {
		t.Fatalf("an oversized request changed the target to %q", got)
	}
}
