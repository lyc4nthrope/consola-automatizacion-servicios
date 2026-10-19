package web

import (
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
)

// Extension points for the modules of Parts A, B and C.
//
// A module lives in its own files and never edits server.go:
//
//	// internal/web/usuarios.go
//	func init() {
//		registerModule("usuarios", func(s *server, r *ModuleRouter) {
//			r.HandleFunc("GET /usuarios", s.usuariosPagina)        // required: the module page
//			r.HandleFunc("POST /usuarios/crear", s.crearUsuario)   // actions: POST, under /usuarios/
//		})
//	}
//
// Its page is web/templates/usuarios.html (a {{define "content"}} block), found automatically.
// Partials in web/templates/_*.html are available to every page.
//
// NewHandler fails at startup, naming the module, when a registered module has no page
// template, does not register GET /<slug>, or registers a route outside /<slug>.

// moduleRoutes registers a module's routes. It receives the server, which gives access to
// s.state, s.runner, s.render, s.newView, s.renderModule and s.runAndShow.
type moduleRoutes func(s *server, r *ModuleRouter)

// moduleRegistry maps a module slug (see modules.go) to its routes. Modules that are not
// registered keep their placeholder page.
var moduleRegistry = map[string]moduleRoutes{}

// registerModule is called from a module's init function. A slug that is not in modules.go
// or is registered twice is a programming error, so it panics at startup.
func registerModule(slug string, reg moduleRoutes) {
	if _, ok := findModule(slug); !ok {
		panic(fmt.Sprintf("registerModule: unknown module %q (add it to modules.go)", slug))
	}
	if _, dup := moduleRegistry[slug]; dup {
		panic(fmt.Sprintf("registerModule: module %q registered twice", slug))
	}
	moduleRegistry[slug] = reg
}

func findModule(slug string) (Module, bool) {
	for _, m := range modules {
		if m.Slug == slug {
			return m, true
		}
	}
	return Module{}, false
}

// ModuleRouter lets a module register routes only under its own path, so modules of
// different parts can never collide.
type ModuleRouter struct {
	mux      *http.ServeMux
	module   Module
	patterns map[string]bool
	err      error
}

// HandleFunc registers "METHOD /path". The method is mandatory (GET for pages, POST for
// actions) and the path must be /<slug> or start with /<slug>/.
func (r *ModuleRouter) HandleFunc(pattern string, h http.HandlerFunc) {
	if r.err != nil {
		return
	}
	method, p, ok := strings.Cut(pattern, " ")
	switch {
	case !ok || !reMethod.MatchString(method): // "get" would never match any request
		r.err = fmt.Errorf("module %q: route %q must start with a method, e.g. \"POST %s/accion\"", r.module.Slug, pattern, r.module.Path())
		return
	case p != r.module.Path() && !strings.HasPrefix(p, r.module.Path()+"/"):
		r.err = fmt.Errorf("module %q: route %q is outside %s/", r.module.Slug, pattern, r.module.Path())
		return
	}
	r.mux.HandleFunc(pattern, h)
	r.patterns[pattern] = true
}

// mountModule runs a module's registration and checks the result.
func (s *server) mountModule(mux *http.ServeMux, m Module, reg moduleRoutes) error {
	r := &ModuleRouter{mux: mux, module: m, patterns: map[string]bool{}}
	reg(s, r)
	switch {
	case r.err != nil:
		return r.err
	case !r.patterns["GET "+m.Path()]:
		return fmt.Errorf("module %q must register \"GET %s\" (its page)", m.Slug, m.Path())
	case s.pages[m.Slug+".html"] == nil:
		return fmt.Errorf("module %q is registered but web/templates/%s.html does not exist", m.Slug, m.Slug)
	}
	return nil
}

// Form carries what the user typed and the errors to show next to each field, so a rejected
// form comes back filled in. Errors[""] is a message for the whole form.
type Form struct {
	Values map[string]string
	Errors map[string]string
}

// FieldErrors turns validation errors into Form.Errors: a *validate.Error goes under its
// field, any other error under "" (form level). Nil errors are skipped; no errors gives nil.
func FieldErrors(errs ...error) map[string]string {
	var out map[string]string
	for _, err := range errs {
		if err == nil {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		var ve *validate.Error
		if errors.As(err, &ve) {
			out[ve.Field] = ve.Msg
		} else {
			out[""] = err.Error()
		}
	}
	return out
}

// field is what the "error-campo" partial receives.
type field struct {
	Name  string
	Error string
}

var (
	reFieldName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	reMethod    = regexp.MustCompile(`^[A-Z]+$`)
)

func checkFieldName(name string) {
	if !reFieldName.MatchString(name) {
		panic(fmt.Sprintf("invalid form field name %q: use lowercase letters, digits and -", name))
	}
}

// campo builds the argument of the "error-campo" partial: {{template "error-campo" (campo .Form "paquete")}}.
func campo(f Form, name string) field {
	checkFieldName(name)
	return field{Name: name, Error: f.Errors[name]}
}

// ariaCampo returns the attributes that link an input to its error message (only when the
// field is invalid) and to optional hint ids: <input id="paquete" name="paquete" {{aria .Form "paquete"}}>
// or, with a hint that is always read, {{aria .Form "paquete" "paquete-ayuda"}}.
func ariaCampo(f Form, name string, hints ...string) template.HTMLAttr {
	checkFieldName(name)
	for _, h := range hints {
		checkFieldName(h)
	}
	ids, attrs := hints, ""
	if f.Errors[name] != "" {
		ids = append([]string{name + "-error"}, hints...)
		attrs = `aria-invalid="true" `
	}
	if len(ids) == 0 {
		return ""
	}
	return template.HTMLAttr(attrs + `aria-describedby="` + strings.Join(ids, " ") + `"`)
}

// renderModule shows a module's own page, web/templates/<slug>.html, with the module's data
// (available as .Data) and the form state (available as .Form).
func (s *server) renderModule(w http.ResponseWriter, r *http.Request, slug string, status int, form Form, data any) {
	m, ok := findModule(slug)
	page := slug + ".html"
	if !ok || s.pages[page] == nil {
		log.Printf("renderModule: no page for module %q (expected web/templates/%s)", slug, page)
		http.Error(w, "La página de este módulo todavía no existe. Revisa el registro de la consola en la terminal.", http.StatusInternalServerError)
		return
	}
	v := s.newView(r, m.Title, m.Slug)
	v.Module = &m
	v.Form = form
	v.Data = data
	s.render(w, status, page, v)
}

// runAndShow runs plan and sends the browser to its result page (POST → redirect → GET).
// Every module action that touches a VM ends with it.
func (s *server) runAndShow(w http.ResponseWriter, r *http.Request, plan operacion.Plan) {
	e, err := s.runner.Run(r.Context(), plan)
	if err != nil {
		log.Printf("%s: %v", plan.Action, err)
		switch {
		case errors.Is(err, operacion.ErrUnknownTarget):
			http.Error(w, "El destino elegido no existe en config.json. Elige otro en la barra superior y vuelve a intentarlo.", http.StatusBadRequest)
			return
		case e.ID == "": // nothing ran: the plan itself is wrong
			http.Error(w, "No se pudo ejecutar la operación. Revisa el registro de la consola en la terminal.", http.StatusInternalServerError)
			return
		}
		// The commands ran but the log could not be written: the result page still shows them.
	}
	http.Redirect(w, r, "/resultado/"+e.ID, http.StatusSeeOther)
}
