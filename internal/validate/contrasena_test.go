package validate

import "testing"

// Contrasena must accept normal passwords and reject whatever chpasswd or the
// terminal would misread: too short/long, ":" (the chpasswd separator), line
// breaks (chpasswd reads line by line) and control characters (DB13).
func TestContrasena(t *testing.T) {
	pruebas := []struct {
		nombre string
		valor  string
		ok     bool
	}{
		{"corto de un carácter", "Segura7:", false},
		{"7 caracteres", "Segura7", false},
		{"justo 8", "Segura71", true},
		{"128 caracteres", stringsRepeat("Segura71", 16), true},
		{"129 caracteres", stringsRepeat("Segura71", 16) + "x", false},
		{"vacía", "", false},
		{"con dos puntos", "Segura71:", false},
		{"con salto de línea", "Segura7\n1", false},
		{"con retorno de carro", "Segura7\r1", false},
		{"con NUL", "Segura\x0071", false},
		{"con tabulador", "Segura\t711", false},
		{"con escape", "Segura\x1b711", false},
		{"con unicode válido", "Contraseña-De-8 ✓✓", true},
	}
	for _, p := range pruebas {
		err := Contrasena(p.valor)
		if p.ok && err != nil {
			t.Errorf("%s: Contrasena(%q) = %v, want nil", p.nombre, p.valor, err)
		}
		if !p.ok && err == nil {
			t.Errorf("%s: Contrasena(%q) = nil, want error", p.nombre, p.valor)
		}
		if !p.ok {
			if ve, isVE := err.(*Error); !isVE {
				t.Errorf("%s: el error no es *validate.Error: %T", p.nombre, err)
			} else if ve.Field != "contrasena" {
				t.Errorf("%s: el error apunta al campo %q, want \"contrasena\"", p.nombre, ve.Field)
			}
		}
	}
}

func stringsRepeat(s string, n int) string {
	out := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
