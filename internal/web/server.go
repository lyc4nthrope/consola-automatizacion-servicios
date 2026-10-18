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
}

// NewHandler returns the console routes.
func NewHandler(d Deps) (http.Handler, error) {
	hosts, err := allowedHosts(d.Listen)
	if err != nil {
		return nil, err
	}
	s := &server{state: d.State, log: d.Log, runner: operacion.New(d.State, d.Log), pages: map[string]*template.Template{}}
	for _, name := range []string{"inicio.html", "modulo.html", "noencontrada.html", "resultado.html"} {
		t, err := template.New("").Funcs(templateFuncs).ParseFS(d.Templates, "templates/layout.html", "templates/"+name)
		if err != nil {
			return nil, fmt.Errorf("parsing template %s: %w", name, err)
		}
		s.pages[name] = t
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.inicio)
	for i := range modules {
		m := modules[i]
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
	e, err := s.runner.Run(r.Context(), operacion.Plan{
		Action:   "Probar conexión",
		Target:   cur.Name,
		Commands: []executor.Command{{Name: "hostname"}},
		Success: func(res []executor.Result) string {
			return "La VM respondió como «" + strings.TrimSpace(res[0].Stdout) + "». El canal de comunicación funciona."
		},
	})
	if err != nil {
		log.Printf("diagnostico: %v", err)
		if e.ID == "" { // nothing ran
			http.Error(w, "No se pudo ejecutar la operación. Revisa el registro de la consola en la terminal.", http.StatusInternalServerError)
			return
		}
		// The command ran but the log could not be written: still show its result.
	}
	http.Redirect(w, r, "/resultado/"+e.ID, http.StatusSeeOther)
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
