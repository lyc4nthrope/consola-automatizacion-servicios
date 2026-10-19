package web

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// paleta returns the palette dialog of a page.
func paleta(t *testing.T, path string) string {
	t.Helper()
	b := body(do(t, http.MethodGet, path))
	i := strings.Index(b, `<dialog class="paleta" id="paleta"`)
	j := strings.Index(b, `</dialog>`)
	if i < 0 || j < i {
		t.Fatalf("%s has no palette", path)
	}
	return b[i : j+len("</dialog>")]
}

func TestPaletteListsEveryPageActionAndTarget(t *testing.T) {
	p := paleta(t, "/servicios")
	if !strings.Contains(p, `<a href="/" tabindex="-1">`) {
		t.Error("the palette lacks the panel")
	}
	for _, m := range modules {
		if !strings.Contains(p, `<a href="`+m.Path()+`" tabindex="-1"><i class="bi `+m.Icon+`" aria-hidden="true"></i> `+m.Title+`</a>`) {
			t.Errorf("the palette lacks the page %s", m.Slug)
		}
		for _, a := range m.Actions {
			href := m.Path()
			if id := m.Anchors[a]; id != "" {
				href += "#" + id
			}
			if !strings.Contains(p, `<a href="`+href+`" tabindex="-1"><i class="bi bi-arrow-return-right" aria-hidden="true"></i> `+a+`</a>`) {
				t.Errorf("the palette lacks the action %q (%s)", a, href)
			}
			if id := m.Anchors[a]; id != "" && !regexp.MustCompile(`data-claves="`+regexp.QuoteMeta(id)+`">\s*<a href="`+regexp.QuoteMeta(href)+`"`).MatchString(p) {
				t.Errorf("the action %q must also be found by its field name %q", a, id)
			}
		}
	}
	for _, target := range newTestState().Targets() {
		if !strings.Contains(p, `form="paleta-destino" name="destino" value="`+target.Name+`"`) || !strings.Contains(p, `data-claves="vm `+target.VM+`"`) {
			t.Errorf("the palette lacks the target %s", target.Name)
		}
	}
	if !strings.Contains(p, `data-paleta="tema"`) {
		t.Error("the palette lacks the theme switch")
	}
	if !strings.Contains(p, `Usar destino <span translate="no">pruebas</span> <span class="mono paleta-detalle" translate="no">192.168.40.60</span></button>
      <span class="paleta-tipo" id="paleta-destino-0-tipo" aria-hidden="true">Destino actual</span>`) {
		t.Error("the current target must be marked")
	}
	// Ids are unique on the whole page (aria-activedescendant and aria-describedby use them).
	for _, path := range []string{"/", "/preparacion"} {
		seen := map[string]bool{}
		for _, id := range regexp.MustCompile(` id="([^"]+)"`).FindAllStringSubmatch(body(do(t, http.MethodGet, path)), -1) {
			if seen[id[1]] {
				t.Errorf("%s: duplicate id %s", path, id[1])
			}
			seen[id[1]] = true
		}
	}
	// The type tag describes the option instead of being read as part of its name.
	if !strings.Contains(p, `id="paleta-panel" aria-selected="false" aria-describedby="paleta-panel-tipo">`) ||
		!strings.Contains(p, `<span class="paleta-tipo" id="paleta-panel-tipo" aria-hidden="true">Página</span>`) {
		t.Error("the option's type must be its description")
	}
	if strings.Count(p, " data-actual") != 1 || !strings.Contains(p, `data-claves="vm consola-pruebas" data-actual>`) {
		t.Error("only the current target is marked data-actual")
	}
}

// The markup offers no operation; that the JavaScript only follows links, changes the
// target or the theme was checked in a browser.
func TestPaletteMarkupOffersNoOperation(t *testing.T) {
	p := paleta(t, "/preparacion")
	forms := regexp.MustCompile(`<form[^>]*>`).FindAllString(p, -1)
	if len(forms) != 1 || forms[0] != `<form id="paleta-destino" method="post" action="/destino">` {
		t.Fatalf("the palette may only post to /destino, found %q", forms)
	}
	if strings.Contains(p, "data-operacion") || strings.Contains(p, "formaction") {
		t.Fatal("no palette option may start an operation")
	}
	if !strings.Contains(p, `<input type="hidden" name="volver" value="/preparacion">`) {
		t.Fatal("changing the target from the palette must come back to the same page")
	}
}

func TestPaletteOpenerNeedsJavaScript(t *testing.T) {
	b := body(do(t, http.MethodGet, "/"))
	if !strings.Contains(b, `id="abrir-paleta" aria-haspopup="dialog" aria-controls="paleta" aria-keyshortcuts="Control+K Meta+K" aria-label="Ir a una página, una acción o un destino (Ctrl+K)" hidden>`) {
		t.Fatal("the opener must be hidden until consola.js shows it, and announce its shortcut")
	}
}

// Every anchor of a module action must exist on its page, so the palette never jumps to
// nothing. This covers the anchors Parts B and C add too.
func TestModuleAnchorsExistOnTheirPages(t *testing.T) {
	for _, m := range modules {
		if len(m.Anchors) == 0 {
			continue
		}
		b := body(do(t, http.MethodGet, m.Path()))
		for action, id := range m.Anchors {
			if !containsString(m.Actions, action) {
				t.Errorf("%s: anchor for %q, which is not one of its actions", m.Slug, action)
			}
			if !strings.Contains(b, ` id="`+id+`"`) {
				t.Errorf("%s: action %q jumps to #%s, which is not on the page", m.Slug, action, id)
			}
		}
	}
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
