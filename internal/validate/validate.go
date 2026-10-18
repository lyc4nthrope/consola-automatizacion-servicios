// Package validate checks user input before any operation runs on a VM.
//
// Every rule is a whitelist: values that reach a command line or a file name can only
// contain characters that have no meaning for a shell. Errors carry a message for the
// user, in Spanish, that says what is wrong and shows a valid example.
package validate

import (
	"net"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Error is a validation error for one form field.
type Error struct {
	Field string // form field name, used to show the message next to it
	Msg   string // message for the user
}

func (e *Error) Error() string { return e.Msg }

var (
	rePaquete  = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{1,99}$`)
	reUsuario  = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	reHostname = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	reServicio = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,99}$`)
)

// Paquete checks a Debian package name (Debian Policy §5.6.1).
func Paquete(s string) error {
	if !rePaquete.MatchString(s) {
		return &Error{"paquete", "El nombre del paquete solo admite minúsculas, números y los signos + . -, empieza con letra o número y tiene al menos 2 caracteres. Ejemplo: tree"}
	}
	return nil
}

// Usuario checks a Linux user name as accepted by Debian's adduser by default.
func Usuario(s string) error {
	if !reUsuario.MatchString(s) {
		return &Error{"usuario", "El nombre de usuario empieza con una minúscula o _, sigue con minúsculas, números, _ o -, y tiene máximo 32 caracteres. Ejemplo: daniel"}
	}
	return nil
}

// Hostname checks a single host name label (RFC 1123), lowercase.
func Hostname(s string) error {
	if !reHostname.MatchString(s) {
		return &Error{"hostname", "El nombre de host usa minúsculas, números y guiones, no empieza ni termina con guion y tiene máximo 63 caracteres. Ejemplo: servidor1"}
	}
	return nil
}

// IPv4 checks a dotted IPv4 address without leading zeros.
func IPv4(s string) error {
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil || ip.String() != s {
		return &Error{"ip", "Escribe una dirección IPv4 completa, sin espacios ni ceros a la izquierda. Ejemplo: 192.168.40.60"}
	}
	return nil
}

// Puerto checks a TCP port number written in canonical form.
func Puerto(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != s {
		return &Error{"puerto", "El puerto es un número entre 1 y 65535, sin signos ni ceros a la izquierda. Ejemplo: 8080"}
	}
	return nil
}

// NombreServicio checks a systemd unit name without dangerous characters.
func NombreServicio(s string) error {
	if !reServicio.MatchString(s) {
		return &Error{"servicio", "El nombre del servicio usa letras, números y los signos _ . @ -, y empieza con letra o número. Ejemplo: bookzilla"}
	}
	return nil
}

// MaxMensaje is the maximum length, in characters, of a free-text message.
const MaxMensaje = 500

// Mensaje checks free text written to a file (e.g. /etc/issue): valid UTF-8, line breaks
// allowed (LF, or CRLF as browsers send them), no other control characters and no
// invisible format characters (zero-width, bidi overrides), at most MaxMensaje characters.
// Write LineasUnix(s) to the file.
func Mensaje(s string) error {
	bad := &Error{"mensaje", "El mensaje admite texto y saltos de línea, sin caracteres de control ni invisibles, con máximo 500 caracteres. Ejemplo: Bienvenido a la VM de pruebas"}
	s = LineasUnix(s)
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxMensaje {
		return bad
	}
	for _, r := range s {
		if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			return bad
		}
	}
	return nil
}

// LineasUnix converts CRLF line breaks (sent by browsers) to LF.
func LineasUnix(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}
