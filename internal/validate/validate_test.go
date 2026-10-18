package validate

import (
	"errors"
	"strings"
	"testing"
)

// injections must be rejected by every rule that ends up in a command line or a file name.
var injections = []string{
	"tree; rm -rf /",
	"$(reboot)",
	"`id`",
	"a && b",
	"a | b",
	"a\nb",
	"a b",
	"-rf",
	"--purge",
	"../etc",
	"a\x00b",
	"",
}

func TestPaquete(t *testing.T) {
	ok := []string{"tree", "openssh-server", "libc6", "g++", "python3.12", "lib32z1"}
	bad := append([]string{"Tree", "a", ".tree", "+tree", "tree_x", strings.Repeat("a", 101)}, injections...)
	check(t, "Paquete", Paquete, ok, bad)
}

func TestUsuario(t *testing.T) {
	ok := []string{"ana", "daniel_p", "_svc", "web-01", strings.Repeat("a", 32)}
	bad := append([]string{"Ana", "1ana", "ana.p", strings.Repeat("a", 33)}, injections...)
	check(t, "Usuario", Usuario, ok, bad)
}

func TestHostname(t *testing.T) {
	ok := []string{"servidor1", "web-01", "a", "x" + strings.Repeat("y", 62)}
	bad := append([]string{"Servidor1", "-web", "web-", "web.local", "web_01", "x" + strings.Repeat("y", 63)}, injections...)
	check(t, "Hostname", Hostname, ok, bad)
}

func TestIPv4(t *testing.T) {
	ok := []string{"192.168.40.60", "10.0.0.1", "127.0.0.1"}
	bad := append([]string{"192.168.40", "256.1.1.1", "::1", "::ffff:192.168.40.60", "192.168.040.60", " 10.0.0.1", "10.0.0.1/24"}, injections...)
	check(t, "IPv4", IPv4, ok, bad)
}

func TestPuerto(t *testing.T) {
	ok := []string{"80", "8080", "65535", "1"}
	bad := append([]string{"0", "65536", "+80", "080", "8o", "http"}, injections...)
	check(t, "Puerto", Puerto, ok, bad)
}

func TestNombreServicio(t *testing.T) {
	ok := []string{"bookzilla", "app_web1", "getty@tty1", "fecha.timer", "Bookzilla"}
	bad := append([]string{".hidden", "@x", strings.Repeat("a", 101)}, injections...)
	check(t, "NombreServicio", NombreServicio, ok, bad)
}

func TestMensaje(t *testing.T) {
	ok := []string{"", "Bienvenido a la VM de pruebas", "Línea 1\nLínea 2", "¡Hola! — acceso restringido", "Usa $HOME y `id` como texto"}
	bad := []string{"a\x00b", "beep\x07", "esc\x1b[31m", "tab\tno", "cr\rsolo", strings.Repeat("a", 501), "\xff\xfe"}
	check(t, "Mensaje", Mensaje, ok, bad)
}

func TestErrorsNameTheFieldAndExplainTheFix(t *testing.T) {
	err := Paquete("tree; rm -rf /")
	var ve *Error
	if !errors.As(err, &ve) {
		t.Fatalf("error %T is not a *validate.Error", err)
	}
	if ve.Field != "paquete" {
		t.Errorf("Field = %q, want paquete", ve.Field)
	}
	if !strings.Contains(ve.Msg, "Ejemplo") {
		t.Errorf("message %q should include an example of a valid value", ve.Msg)
	}
	if err.Error() != ve.Msg {
		t.Errorf("Error() = %q, want the user message", err.Error())
	}
}

func check(t *testing.T, name string, fn func(string) error, ok, bad []string) {
	t.Helper()
	for _, v := range ok {
		if err := fn(v); err != nil {
			t.Errorf("%s(%q) rejected a valid value: %v", name, v, err)
		}
	}
	for _, v := range bad {
		if err := fn(v); err == nil {
			t.Errorf("%s(%q) accepted an invalid value", name, v)
		}
	}
}

func TestMensajeAcceptsBrowserLineBreaks(t *testing.T) {
	// Browsers send textarea line breaks as CRLF.
	if err := Mensaje("Línea 1\r\nLínea 2"); err != nil {
		t.Fatalf("CRLF from a form must be accepted: %v", err)
	}
	if got := LineasUnix("a\r\nb\r\n"); got != "a\nb\n" {
		t.Fatalf("LineasUnix = %q", got)
	}
	for _, bad := range []string{"solo\rcr", "bidi\u202eoculto", "cero\u200bancho"} {
		if err := Mensaje(bad); err == nil {
			t.Errorf("Mensaje(%q) accepted a lone CR or an invisible format character", bad)
		}
	}
}
