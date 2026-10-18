// Package web is the HTTP adapter of the console: routes, handlers, templates and static files.
package web

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
)

// Version is shown in the status bar.
const Version = "v0.2"

// Deps are the parts the HTTP layer needs.
type Deps struct {
	Static    fs.FS      // must contain the "static" directory
	Templates fs.FS      // must contain the "templates" directory
	State     *app.State // targets and active channels
	Log       *oplog.Log // operation log
	Listen    string     // host:port the server listens on; other Host headers are refused
}

// server holds what the handlers need.
type server struct {
	state  *app.State
	log    *oplog.Log
	runner *operacion.Runner
	pages  map[string]*template.Template
}

var reOperationID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// templateFuncs format values for display.
var templateFuncs = template.FuncMap{
	"ms":   func(ms int64) string { return formatDuration(time.Duration(ms) * time.Millisecond) },
	"dur":  formatDuration,
	"hora": func(t time.Time) string { return t.Format("15:04:05") },
	"fecha": func(t time.Time) string {
		return t.Format("02/01/2006 15:04:05")
	},
	"campo": campo,
	"aria":  ariaCampo,
}

// formatDuration writes durations the way the interface shows them: "320 ms", "4,2 s", "2 min 5 s".
func formatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < time.Minute:
		return strings.Replace(fmt.Sprintf("%.1f s", d.Seconds()), ".", ",", 1)
	default:
		d = d.Round(time.Second)
		return fmt.Sprintf("%d min %d s", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// view is the data every page template receives.
type view struct {
	Title   string
	Path    string  // current path, used to come back after changing the target
	Active  string  // slug of the current section, "" for the panel
	Module  *Module // set on module pages
	Modules []Module
	Targets []app.Target
	Current app.Target
	Channel string
	Recent  []oplog.Entry // newest first, for the panel
	Last    *oplog.Entry  // last operation, for the status bar
	Entry   *oplog.Entry  // set on the result page
	Version string
	Form    Form // module pages: values typed and errors per field
	Data    any  // module pages: whatever the module passes to its template
}

// NewHandler returns the console routes.
func NewHandler(d Deps) (http.Handler, error) {
	hosts, err := allowedHosts(d.Listen)
	if err != nil {
		return nil, err
	}
	s := &server{state: d.State, log: d.Log, runner: operacion.New(d.State, d.Log)}
	if s.pages, err = parsePages(d.Templates); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.inicio)
	for i := range modules {
		m := modules[i]
		if reg := moduleRegistry[m.Slug]; reg != nil {
			// The module's own routes and page (see registro.go).
			if err := s.mountModule(mux, m, reg); err != nil {
				return nil, err
			}
			continue
		}
		mux.HandleFunc("GET "+m.Path(), func(w http.ResponseWriter, r *http.Request) { s.modulo(w, r, &m) })
	}
	mux.HandleFunc("POST /destino", s.seleccionarDestino)
	mux.HandleFunc("POST /diagnostico", s.diagnostico)
	mux.HandleFunc("GET /resultado/{id}", s.resultado)
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("GET /static/", staticFiles(http.FileServerFS(d.Static)))
	mux.HandleFunc("GET /", s.noEncontrada)

	return securityHeaders(onlyHosts(hosts, sameOrigin(mux))), nil
}

// parsePages parses every page in templates/: each templates/<name>.html (except layout.html
// and partials) together with the layout and every partial templates/_*.html. New pages and
// partials are picked up without changing code.
//
// Mistakes fail at startup instead of on the first request: a page without a "content"
// block, a partial that defines "content" or "layout", and a page that redefines a name
// owned by the layout or a partial (it would silently replace it on that page).
func parsePages(templates fs.FS) (map[string]*template.Template, error) {
	files, err := fs.Glob(templates, "templates/*.html")
	if err != nil {
		return nil, err
	}
	shared := []string{"templates/layout.html"}
	var pages []string
	for _, f := range files {
		switch base := path.Base(f); {
		case base == "layout.html":
		case strings.HasPrefix(base, "_"):
			shared = append(shared, f)
		default:
			pages = append(pages, f)
		}
	}

	owned := map[string]string{} // template name -> file that defines it
	for _, f := range shared {
		names, err := definedNames(templates, f)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if f != "templates/layout.html" && (n == "content" || n == "layout") {
				return nil, fmt.Errorf("partial %s must not define %q", f, n)
			}
			owned[n] = f
		}
	}

	out := make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		names, err := definedNames(templates, p)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if other, ok := owned[n]; ok {
				return nil, fmt.Errorf("page %s redefines %q, already defined in %s", p, n, other)
			}
		}
		t, err := template.New("").Funcs(templateFuncs).ParseFS(templates, append(shared, p)...)
		if err != nil {
			return nil, fmt.Errorf("parsing template %s: %w", p, err)
		}
		if !slices.Contains(names, "content") {
			return nil, fmt.Errorf("page %s must define {{define \"content\"}}", p)
		}
		out[path.Base(p)] = t
	}
	return out, nil
}

// definedNames lists the {{define}} blocks of one template file.
func definedNames(templates fs.FS, file string) ([]string, error) {
	src, err := fs.ReadFile(templates, file)
	if err != nil {
		return nil, err
	}
	t, err := template.New(file).Funcs(templateFuncs).Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("parsing template %s: %w", file, err)
	}
	var names []string
	for _, d := range t.Templates() {
		if d.Name() != file {
			names = append(names, d.Name())
		}
	}
	return names, nil
}

// allowedHosts returns the Host header values that address this console: the listen
// address itself and localhost on the same port.
func allowedHosts(listen string) (map[string]bool, error) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return nil, fmt.Errorf("listen address %q: %w", listen, err)
	}
	return map[string]bool{listen: true, net.JoinHostPort("localhost", port): true}, nil
}

// onlyHosts blocks DNS rebinding: a malicious site can make its own domain resolve to
// 127.0.0.1, and its requests then look same-origin to the browser. They still carry the
// attacker's domain in the Host header, so only the console's own addresses are accepted,
// for every method (rebinding could also read the pages).
func onlyHosts(hosts map[string]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hosts[r.Host] {
			http.Error(w, "Dirección no reconocida. Abre la consola en http://127.0.0.1 con el puerto configurado.", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) newView(r *http.Request, title, active string) view {
	cur := s.state.Current()
	v := view{
		Title:   title,
		Path:    r.URL.Path,
		Active:  active,
		Modules: modules,
		Targets: s.state.Targets(),
		Current: cur,
		Channel: s.state.Channel(cur.Name),
		Version: Version,
	}
	if s.log != nil {
		v.Recent = s.log.Recent(5)
		if len(v.Recent) > 0 {
			v.Last = &v.Recent[0]
		}
	}
	return v
}

// diagnostico runs "hostname" on the current target to check that its channel works.
func (s *server) diagnostico(w http.ResponseWriter, r *http.Request) {
	cur := s.state.Current()
	s.runAndShow(w, r, operacion.Plan{
		Action:   "Probar conexión",
		Target:   cur.Name,
		Commands: []executor.Command{{Name: "hostname"}},
		Success: func(res []executor.Result) string {
			return "La VM respondió como «" + strings.TrimSpace(res[0].Stdout) + "». El canal de comunicación funciona."
		},
	})
}

// resultado shows one operation: the outcome for the user and the terminal with every step.
func (s *server) resultado(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	e, ok := oplog.Entry{}, false
	if reOperationID.MatchString(id) {
		e, ok = s.log.Get(id)
	}
	if !ok {
		s.noEncontrada(w, r)
		return
	}
	outcome := "Éxito"
	if !e.OK {
		outcome = "Falló"
	}
	v := s.newView(r, outcome+" · "+e.Action, "-")
	v.Entry = &e
	s.render(w, http.StatusOK, "resultado.html", v)
}

func (s *server) inicio(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "inicio.html", s.newView(r, "Panel", ""))
}

func (s *server) modulo(w http.ResponseWriter, r *http.Request, m *Module) {
	v := s.newView(r, m.Title, m.Slug)
	v.Module = m
	s.render(w, http.StatusOK, "modulo.html", v)
}

func (s *server) noEncontrada(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusNotFound, "noencontrada.html", s.newView(r, "Página no encontrada", "-"))
}

// seleccionarDestino changes the target the console works on and goes back to the page.
func (s *server) seleccionarDestino(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := s.state.SelectTarget(r.PostFormValue("destino")); err != nil {
		http.Error(w, "Destino desconocido. Elige uno de la lista.", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, safeBack(r.PostFormValue("volver")), http.StatusSeeOther)
}

// safeBack only allows returning to a page of the console, never to another site.
func safeBack(p string) string {
	for _, m := range modules {
		if p == m.Path() {
			return p
		}
	}
	return "/"
}

// render executes the page into a buffer first, so a template error never sends half a page.
func (s *server) render(w http.ResponseWriter, status int, page string, v view) {
	var buf bytes.Buffer
	if err := s.pages[page].ExecuteTemplate(&buf, "layout", v); err != nil {
		log.Printf("rendering %s: %v", page, err)
		http.Error(w, "Error interno al mostrar la página. Recarga; si se repite, revisa el registro de la consola en la terminal.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// sameOrigin rejects state-changing requests sent by other sites (CSRF). The console runs
// commands as root, so a page open in another tab must not be able to submit its forms.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				http.Error(w, "Solicitud rechazada: llegó desde otro sitio web. Abre la consola en http://"+r.Host+" y envía el formulario desde ahí.", http.StatusForbidden)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					http.Error(w, "Solicitud rechazada: llegó desde otro sitio web. Abre la consola en http://"+r.Host+" y envía el formulario desde ahí.", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders forbids framing (clickjacking) and loading anything from other hosts.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
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
		next.ServeHTTP(w, r)
	})
}
