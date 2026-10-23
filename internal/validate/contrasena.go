package validate

import (
	"unicode"
	"unicode/utf8"
)

// Contrasena checks a VM account password (DB13): 8 to 128 characters, without
// ":" or line breaks or control characters. chpasswd receives "usuario:contrasena"
// on a line and uses ":" as the separator, so anything else it could misread is
// rejected before it reaches the channel.
func Contrasena(s string) error {
	bad := &Error{"contrasena", "La contraseña tiene entre 8 y 128 caracteres, sin «:», sin saltos de línea y sin caracteres de control. Ejemplo: ClaveSegura-2026"}
	n := utf8.RuneCountInString(s)
	if n < 8 || n > 128 {
		return bad
	}
	for _, r := range s {
		if r == ':' || r == '\n' || r == '\r' || isControl(r) {
			return bad
		}
	}
	return nil
}

// isControl mirrors unicode.IsControl for the control plane (C0 and DEL) plus
// the direction/format invisible characters Mensaje already rejects: they are
// invisible or reorder text, which a password field never needs.
func isControl(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}
