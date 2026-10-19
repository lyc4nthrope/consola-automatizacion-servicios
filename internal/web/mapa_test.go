package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
)

func TestChannelKind(t *testing.T) {
	for channel, want := range map[string]string{
		"Guest Additions": "ga", "SSH": "ssh", "Simulado": "sim", "Sin conexión": "none", "Otro canal": "otro",
	} {
		if got := channelKind(channel); got != want {
			t.Errorf("channelKind(%q) = %q, want %q", channel, got, want)
		}
	}
}

// mapItems returns, in page order, each VM item of the map: its classes and its button.
var reMapItem = regexp.MustCompile(`(?s)<li class="mapa-rama mapa-vm ([^"]+)">\s*<button class="mapa-boton" type="submit" name="destino" value="([^"]+)"([^>]*)>(.*?)</button>`)

func mapItems(t *testing.T, b string, want int) [][]string {
	t.Helper()
	items := reMapItem.FindAllStringSubmatch(b, -1)
	if len(items) != want {
		t.Fatalf("%d VMs on the map, want %d (one per config.json target, in order)", len(items), want)
	}
	return items
}

func TestMapShowsEveryVMWithItsRealChannel(t *testing.T) {
	state := app.NewState(app.Config{Listen: app.DefaultListen, Targets: []app.Target{
		{Name: "pruebas", VM: "consola-pruebas", IP: "192.168.40.57"},
		{Name: "balanceador", VM: "balanceador", IP: "192.168.40.53"},
		{Name: "web1", VM: "servidor1", IP: "192.168.40.61"},
		{Name: "web2", VM: "servidor2", IP: "192.168.40.62"},
	}})
	_ = state.SetExecutor("pruebas", &executor.Fake{NameValue: "Guest Additions"})
	_ = state.SetExecutor("web1", &executor.Fake{NameValue: "SSH"})
	_ = state.SetExecutor("web2", executor.NewSimulated("web2"))
	_ = state.SelectTarget("web1")
	b := body(serve(t, newTestHandler(t, state), httptestGet("/")))

	want := []struct{ name, classes, current, text string }{
		{"pruebas", "canal-ga", "", "consola-pruebas · 192.168.40.57"},
		{"balanceador", "canal-none", "", "Sin conexión"},
		{"web1", "canal-ssh is-current", ` aria-current="true"`, "SSH"},
		{"web2", "canal-sim", "", "Simulado"},
	}
	items := mapItems(t, b, len(want))
	for i, w := range want {
		if !strings.Contains(items[i][4], `<span class="visually-hidden">Usar como destino:</span>`) {
			t.Errorf("%s: the button must say what it does", w.name)
		}
		got := items[i]
		if got[2] != w.name || got[1] != w.classes || got[3] != w.current || !strings.Contains(got[4], w.text) {
			t.Errorf("VM %d = %q classes %q attrs %q, want %s %q %q containing %q", i, got[2], got[1], got[3], w.name, w.classes, w.current, w.text)
		}
		if has := strings.Contains(got[4], "Destino actual"); has != (w.current != "") {
			t.Errorf("%s: 'Destino actual' shown = %v", w.name, has)
		}
	}
	if !strings.Contains(b, "4 VMs configuradas en config.json") {
		t.Error("the VirtualBox node must count the VMs")
	}
	for _, legend := range []string{"Guest Additions</li>", "SSH</li>", "Simulado</li>", "Sin conexión (sin línea)</li>"} {
		if !strings.Contains(b, legend) {
			t.Errorf("legend lacks %q", legend)
		}
	}
}

func TestMapWithOneVMAndEscapedValues(t *testing.T) {
	state := app.NewState(app.Config{Listen: app.DefaultListen, Targets: []app.Target{{Name: `a"><b>`, VM: "<vm>", IP: "10.0.0.1"}}})
	_ = state.SetExecutor(`a"><b>`, &executor.Fake{NameValue: "Telepatía"})
	b := body(serve(t, newTestHandler(t, state), httptestGet("/")))
	if !strings.Contains(b, "1 VM configurada en config.json") {
		t.Error("singular count")
	}
	if strings.Contains(b, "<vm>") || strings.Contains(b, `"><b>`) || !strings.Contains(b, "&lt;vm&gt; · 10.0.0.1") ||
		!strings.Contains(b, `value="a&#34;&gt;&lt;b&gt;"`) || !strings.Contains(b, `translate="no">a&#34;&gt;&lt;b&gt;</span>`) {
		t.Error("config values must be escaped on the map, in attributes and in text")
	}
	if items := mapItems(t, b, 1); items[0][1] != "canal-otro is-current" || !strings.Contains(items[0][4], "Telepatía") {
		t.Errorf("an unknown channel keeps its own name and line: %q", items[0][1])
	}
}

func TestChoosingAVMOnTheMap(t *testing.T) {
	state := newTestState()
	h := newTestHandler(t, state)
	b := body(serve(t, h, httptestGet("/")))
	if !strings.Contains(b, `<form class="mapa" method="post" action="/destino">`) {
		t.Fatal("the map must post to /destino")
	}
	// What the browser sends when the balanceador button is pressed; it comes back to the panel.
	rec := serve(t, h, postForm("/destino", url.Values{"destino": {"balanceador"}}))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" || state.Current().Name != "balanceador" {
		t.Fatalf("status %d Location %q current %q", rec.Code, rec.Header().Get("Location"), state.Current().Name)
	}
	if items := mapItems(t, body(serve(t, h, httptestGet("/"))), 2); items[1][3] != ` aria-current="true"` || items[0][3] != "" {
		t.Fatal("after choosing, the map marks balanceador as the current target")
	}
}

func TestMapIsOnlyOnThePanel(t *testing.T) {
	if b := body(do(t, http.MethodGet, "/preparacion")); strings.Contains(b, `class="mapa"`) {
		t.Fatal("the map belongs to the panel")
	}
}
