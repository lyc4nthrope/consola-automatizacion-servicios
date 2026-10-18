package web

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
	assets "github.com/lyc4nthrope/consola-automatizacion-servicios/web"
)

// These tests use test-only modules, so they never collide with the real registrations of
// Parts A, B and C (usuarios, servicios, balanceador, preparacion).

// withTestModule adds a module that exists only for this test and, if reg is not nil,
// registers its routes. Everything is undone when the test ends.
func withTestModule(t *testing.T, slug string, reg moduleRoutes) {
	t.Helper()
	saved := modules
	modules = append(append([]Module(nil), modules...), Module{Slug: slug, Title: "Módulo " + slug, Icon: "bi-gear", Summary: "Solo para pruebas.", Actions: []string{"Probar"}})
	if reg != nil {
		registerModule(slug, reg)
	}
	t.Cleanup(func() {
		modules = saved
		delete(moduleRegistry, slug)
	})
}

// templatesWith returns the real templates plus extra files, as module authors add them.
func templatesWith(t *testing.T, extra map[string]string) fs.FS {
	t.Helper()
	m := fstest.MapFS{}
	err := fs.WalkDir(assets.Templates, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(assets.Templates, p)
		m[p] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range extra {
		m["templates/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func newHandlerWith(t *testing.T, templates fs.FS) (http.Handler, error) {
	t.Helper()
	log, err := oplog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	state := newTestState()
	_ = state.SetExecutor("pruebas", executor.NewSimulated("pruebas"))
	return NewHandler(Deps{Static: assets.Static, Templates: templates, State: state, Log: log, Listen: testListen})
}

func mustHandlerWith(t *testing.T, templates fs.FS) http.Handler {
	t.Helper()
	h, err := newHandlerWith(t, templates)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

const pageTpl = `{{define "content"}}<h1>{{.Module.Title}}</h1>{{end}}`

func TestRegisteredModuleReplacesItsPlaceholder(t *testing.T) {
	withTestModule(t, "mod-propio", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /mod-propio", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("pagina propia")) })
	})
	withTestModule(t, "mod-sin-registrar", nil)
	h := mustHandlerWith(t, templatesWith(t, map[string]string{"mod-propio.html": pageTpl}))
	if got := body(serve(t, h, httptest.NewRequest(http.MethodGet, "/mod-propio", nil))); got != "pagina propia" {
		t.Fatalf("GET /mod-propio = %q, want the module's own page", got)
	}
	if page := body(serve(t, h, httptest.NewRequest(http.MethodGet, "/mod-sin-registrar", nil))); !strings.Contains(page, "en construcción") {
		t.Fatal("a module that did not register keeps its placeholder page")
	}
}

func TestRegisterModuleRejectsMistakes(t *testing.T) {
	expectPanic := func(t *testing.T, slug string) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("registerModule(%q) must panic: it is a programming error caught at startup", slug)
			}
		}()
		registerModule(slug, func(*server, *ModuleRouter) {})
	}
	t.Run("unknown slug", func(t *testing.T) { expectPanic(t, "no-existe-en-modules") })
	t.Run("duplicate", func(t *testing.T) {
		withTestModule(t, "mod-doble", func(*server, *ModuleRouter) {})
		expectPanic(t, "mod-doble")
	})
}

func TestModuleMistakesStopTheConsoleAtStartup(t *testing.T) {
	cases := []struct {
		name    string
		reg     moduleRoutes
		page    bool
		wantErr string
	}{
		{"route outside its prefix", func(s *server, r *ModuleRouter) {
			r.HandleFunc("GET /mod-x", http.NotFound)
			r.HandleFunc("POST /otro-modulo/accion", http.NotFound)
		}, true, "outside /mod-x/"},
		{"prefix look-alike", func(s *server, r *ModuleRouter) {
			r.HandleFunc("GET /mod-x", http.NotFound)
			r.HandleFunc("POST /mod-xyz", http.NotFound)
		}, true, "outside /mod-x/"},
		{"route without method", func(s *server, r *ModuleRouter) { r.HandleFunc("/mod-x", http.NotFound) }, true, "must start with a method"},
		{"lowercase method", func(s *server, r *ModuleRouter) { r.HandleFunc("get /mod-x", http.NotFound) }, true, "must start with a method"},
		{"missing page route", func(s *server, r *ModuleRouter) { r.HandleFunc("POST /mod-x/accion", http.NotFound) }, true, `must register "GET /mod-x"`},
		{"missing page template", func(s *server, r *ModuleRouter) { r.HandleFunc("GET /mod-x", http.NotFound) }, false, "web/templates/mod-x.html does not exist"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withTestModule(t, "mod-x", c.reg)
			extra := map[string]string{}
			if c.page {
				extra["mod-x.html"] = pageTpl
			}
			_, err := newHandlerWith(t, templatesWith(t, extra))
			if err == nil || !strings.Contains(err.Error(), c.wantErr) || !strings.Contains(err.Error(), "mod-x") {
				t.Fatalf("NewHandler error = %v, want one naming the module and containing %q", err, c.wantErr)
			}
		})
	}
}

func TestTemplateMistakesStopTheConsoleAtStartup(t *testing.T) {
	cases := map[string]struct {
		files   map[string]string
		wantErr string
	}{
		"page without content":         {map[string]string{"pagina.html": `{{define "otra"}}x{{end}}`}, `must define {{define "content"}}`},
		"partial defines content":      {map[string]string{"_malo.html": `{{define "content"}}x{{end}}`}, `must not define "content"`},
		"page overrides a partial":     {map[string]string{"pagina.html": `{{define "content"}}x{{end}}{{define "error-campo"}}y{{end}}`}, `redefines "error-campo"`},
		"page overrides the layout":    {map[string]string{"pagina.html": `{{define "content"}}x{{end}}{{define "layout"}}y{{end}}`}, `redefines "layout"`},
		"partial overrides the layout": {map[string]string{"_malo.html": `{{define "layout"}}x{{end}}`}, `must not define "layout"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := newHandlerWith(t, templatesWith(t, c.files))
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("NewHandler error = %v, want %q", err, c.wantErr)
			}
		})
	}
}

func TestModuleTemplatesAndPartialsAreDiscovered(t *testing.T) {
	templates := templatesWith(t, map[string]string{
		"_prueba.html": `{{define "saludo"}}hola desde un parcial{{end}}`,
		"mod-a.html":   `{{define "content"}}<h1>{{.Module.Title}}</h1><p>{{.Data}}</p>{{template "saludo"}}{{end}}`,
	})
	withTestModule(t, "mod-a", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /mod-a", func(w http.ResponseWriter, req *http.Request) {
			s.renderModule(w, req, "mod-a", http.StatusOK, Form{}, "datos del módulo")
		})
	})
	page := body(serve(t, mustHandlerWith(t, templates), httptest.NewRequest(http.MethodGet, "/mod-a", nil)))
	for _, want := range []string{"datos del módulo", "hola desde un parcial", `href="/mod-a" aria-current="page"`, "<title>Módulo mod-a"} {
		if !strings.Contains(page, want) {
			t.Errorf("module page is missing %q", want)
		}
	}
}

func TestRejectedFormComesBackFilledAndLinkedToItsErrors(t *testing.T) {
	templates := templatesWith(t, map[string]string{
		"mod-f.html": `{{define "content"}}<h1>F</h1>{{template "resumen-errores" .Form}}` +
			`<input id="servicio" name="servicio" value="{{index .Form.Values "servicio"}}" {{aria .Form "servicio"}}>` +
			`{{template "error-campo" (campo .Form "servicio")}}` +
			`<input id="puerto" name="puerto" {{aria .Form "puerto"}}>{{end}}`,
	})
	withTestModule(t, "mod-f", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /mod-f", http.NotFound)
		r.HandleFunc("POST /mod-f/guardar", func(w http.ResponseWriter, req *http.Request) {
			v := req.PostFormValue("servicio")
			s.renderModule(w, req, "mod-f", http.StatusUnprocessableEntity,
				Form{Values: map[string]string{"servicio": v}, Errors: FieldErrors(validate.NombreServicio(v))}, nil)
		})
	})
	rec := serve(t, mustHandlerWith(t, templates), postForm("/mod-f/guardar", url.Values{"servicio": {"<x>; rm"}}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	page := body(rec)
	for _, want := range []string{
		`value="&lt;x&gt;; rm"`, // what the user typed comes back, escaped
		`value="&lt;x&gt;; rm" aria-invalid="true" aria-describedby="servicio-error"`, // the input points to its message
		`<p class="field-error" id="servicio-error">`,                                 // the message exists with that id
		`role="alert"`,                       // the summary is announced
		`<a href="#servicio">`,               // and links to the field
		"El nombre del servicio",             // the validation message itself
		`<input id="puerto" name="puerto" >`, // a valid field gets no aria attributes
	} {
		if !strings.Contains(page, want) {
			t.Errorf("form page is missing %q", want)
		}
	}
}

func TestFieldNamesUsedInTemplatesAreChecked(t *testing.T) {
	templates := templatesWith(t, map[string]string{
		"mod-n.html": `{{define "content"}}<h1>N</h1><input {{aria .Form "a onfocus=x"}}>{{end}}`,
	})
	withTestModule(t, "mod-n", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /mod-n", func(w http.ResponseWriter, req *http.Request) {
			s.renderModule(w, req, "mod-n", http.StatusOK, Form{Errors: map[string]string{"a onfocus=x": "e"}}, nil)
		})
	})
	rec := serve(t, mustHandlerWith(t, templates), httptest.NewRequest(http.MethodGet, "/mod-n", nil))
	if rec.Code != http.StatusInternalServerError || strings.Contains(body(rec), "onfocus") {
		t.Fatalf("status %d: a field name outside [a-z0-9-] must not reach the HTML attributes", rec.Code)
	}
}

func TestFieldErrors(t *testing.T) {
	got := FieldErrors(nil, validate.Paquete("x;y"), errors.New("fallo general"), validate.Puerto("0"))
	if !strings.HasPrefix(got["paquete"], "El nombre del paquete") || !strings.HasPrefix(got["puerto"], "El puerto") {
		t.Fatalf("field errors = %v", got)
	}
	if got[""] != "fallo general" {
		t.Fatalf(`an error without field must go under "" (form level), got %v`, got)
	}
	if FieldErrors(nil, nil) != nil {
		t.Fatal("no errors must give a nil map, so templates treat the form as valid")
	}
}

func TestRunAndShowRedirectsToTheResult(t *testing.T) {
	withTestModule(t, "mod-r", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /mod-r", http.NotFound)
		r.HandleFunc("POST /mod-r/probar", func(w http.ResponseWriter, req *http.Request) {
			s.runAndShow(w, req, operacion.Plan{Action: "Probar módulo", Target: s.state.Current().Name,
				Commands: []executor.Command{{Name: "hostname"}}})
		})
	})
	h := mustHandlerWith(t, templatesWith(t, map[string]string{"mod-r.html": pageTpl}))
	rec := serve(t, h, postForm("/mod-r/probar", url.Values{}))
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/resultado/") {
		t.Fatalf("status %d, Location %q: want 303 to /resultado/<id>", rec.Code, rec.Header().Get("Location"))
	}
	if page := body(serve(t, h, httptest.NewRequest(http.MethodGet, rec.Header().Get("Location"), nil))); !strings.Contains(page, "Probar módulo") {
		t.Fatal("the result page must show the operation")
	}
}

func TestRunAndShowExplainsPlanProblems(t *testing.T) {
	withTestModule(t, "mod-e", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /mod-e", http.NotFound)
		r.HandleFunc("POST /mod-e/vacio", func(w http.ResponseWriter, req *http.Request) {
			s.runAndShow(w, req, operacion.Plan{Action: "x", Target: "pruebas"}) // no commands: programming error
		})
		r.HandleFunc("POST /mod-e/destino", func(w http.ResponseWriter, req *http.Request) {
			s.runAndShow(w, req, operacion.Plan{Action: "x", Target: "borrado", Commands: []executor.Command{{Name: "hostname"}}})
		})
	})
	h := mustHandlerWith(t, templatesWith(t, map[string]string{"mod-e.html": pageTpl}))
	if rec := serve(t, h, postForm("/mod-e/vacio", url.Values{})); rec.Code != http.StatusInternalServerError {
		t.Fatalf("empty plan: status = %d, want 500", rec.Code)
	}
	rec := serve(t, h, postForm("/mod-e/destino", url.Values{}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(body(rec), "Elige otro en la barra superior") {
		t.Fatalf("unknown target: status = %d, want 400 with advice", rec.Code)
	}
}

func TestRenderModuleWithoutItsTemplateIsAClearError(t *testing.T) {
	// A registered module is checked at startup; this covers a handler that renders
	// another, unregistered module whose page does not exist.
	withTestModule(t, "mod-m", nil)
	withTestModule(t, "mod-h", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /mod-h", func(w http.ResponseWriter, req *http.Request) {
			s.renderModule(w, req, "mod-m", http.StatusOK, Form{}, nil)
		})
	})
	h := mustHandlerWith(t, templatesWith(t, map[string]string{"mod-h.html": pageTpl}))
	if rec := serve(t, h, httptest.NewRequest(http.MethodGet, "/mod-h", nil)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when templates/mod-m.html does not exist", rec.Code)
	}
}

func TestPartialsAreEmbeddedInTheBinary(t *testing.T) {
	// go:embed skips files starting with "_" unless the directive uses "all:".
	if _, err := fs.Stat(assets.Templates, "templates/_formulario.html"); err != nil {
		t.Fatalf("the form partials are not embedded: %v", err)
	}
}
