package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
)

// Part A, section 3.1.3: the message the VM console shows before the login prompt
// (/etc/issue, read by agetty).

// issueDato is a piece of live data agetty writes into the message.
type issueDato struct {
	Field  string // form checkbox name
	Label  string // shown in the form and in the message
	Escape string // agetty escape that agetty replaces with the value
}

// issueDatos is the single list behind the form, the file and the preview, in the order
// the message shows them.
var issueDatos = []issueDato{
	{"dato-equipo", "Nombre del equipo", `\n`},
	{"dato-sistema", "Sistema operativo", `\S`},
	{"dato-ip", "Dirección IP", `\4`},
	{"dato-fecha", "Fecha", `\d`},
	{"dato-hora", "Hora", `\t`},
	{"dato-terminal", "Terminal", `\l`},
}

// issueFile is the file agetty reads. The original is kept next to it with the
// ".consola-original" suffix.
const issueFile = "/etc/issue"

// Fixed scripts; the file content and path are positional parameters, never part of a
// script.
const (
	// writeFileScript (used for every file the console writes on the VM) writes "$1" to
	// the file "$2", replacing it atomically (a temporary file of its own + mv), mode 644.
	// With "$3", the SHA-256 of the content the console read, it first checks that the
	// file still has exactly that content and exits 12 otherwise, so an incomplete read or
	// another change made meanwhile is never overwritten. Before the first change it keeps
	// the VM's file as the original (an empty one if there was none). Debian's /etc/issue
	// and /etc/hosts are regular files; a symlink would be replaced by a file.
	writeFileScript = `set -e; umask 022
if [ -n "$3" ]; then
  c=$(sha256sum < "$2") || exit 12
  [ "${c%% *}" = "$3" ] || exit 12
fi
if [ ! -e "$2.consola-original" ]; then
  if [ -e "$2" ]; then cp -p "$2" "$2.consola-original"; else : > "$2.consola-original"; fi
fi
t=$(mktemp "$2.consola-XXXXXX")
trap 'rm -f "$t"' EXIT
printf '%s' "$1" > "$t"
chmod 644 "$t"
mv "$t" "$2"`

	// restoreFileScript puts the original of the file "$1" back atomically and forgets
	// it, so a later change keeps whatever the VM has then; 10 = no saved original.
	restoreFileScript = `set -e
[ -e "$1.consola-original" ] || exit 10
t=$(mktemp "$1.consola-XXXXXX")
trap 'rm -f "$t"' EXIT
cp -p "$1.consola-original" "$t"
mv "$t" "$1"
rm -f "$1.consola-original"`

	// showIssueScript prints the message exactly as agetty shows it. agetty needs a
	// terminal, so script(1) gives it one; the console's agetty has no locale (dates in
	// English), so neither does this one, keeping UTF-8 for accents.
	// The output is captured first so a failure keeps its exit code (script -e returns
	// agetty's), instead of the code of tr at the end of a pipe.
	showIssueScript = `out=$(LC_ALL=C.UTF-8 script -eqc 'agetty --show-issue' /dev/null) || exit $?
printf '%s\n' "$out" | tr -d '\r'`

	noOriginalCode = 10
)

// issueContent builds /etc/issue from the user's message and the chosen data. Every
// backslash the user wrote is doubled, so agetty prints it instead of reading it as an
// escape.
func issueContent(msg string, chosen []issueDato) string {
	msg = strings.TrimRight(validate.LineasUnix(msg), "\n")
	if strings.TrimSpace(msg) == "" {
		msg = ""
	}
	var b strings.Builder
	if msg != "" {
		b.WriteString(strings.ReplaceAll(msg, `\`, `\\`) + "\n")
		if len(chosen) > 0 {
			b.WriteString("\n")
		}
	}
	for _, d := range chosen {
		fmt.Fprintf(&b, "%-19s %s\n", d.Label+":", d.Escape)
	}
	b.WriteString("\n") // blank line before "login:", like Debian's own /etc/issue
	return b.String()
}

// renderIssue shows content the way agetty would, with values for its escapes.
func renderIssue(content string, values map[byte]string) string {
	var b strings.Builder
	for i := 0; i < len(content); i++ {
		if content[i] != '\\' || i+1 == len(content) {
			b.WriteByte(content[i])
			continue
		}
		next := content[i+1]
		switch v, ok := values[next]; {
		case next == '\\':
			b.WriteByte('\\')
		case ok:
			b.WriteString(v)
		default:
			b.WriteString(content[i : i+2])
		}
		i++
	}
	return b.String()
}

// exampleValues are the preview values: the target's own name and IP, and the date and
// time as agetty writes them.
func exampleValues(t app.Target, now time.Time) map[byte]string {
	return map[byte]string{
		'n': t.VM, 'S': "Debian GNU/Linux 13 (trixie)", '4': t.IP,
		'd': now.Format("Mon Jan _2  2006"), 't': now.Format("15:04:05"), 'l': "tty1",
	}
}

// issuePreview is what the page shows after "Vista previa".
type issuePreview struct {
	Archivo string // content of /etc/issue
	Ejemplo string // how the VM console would show it
}

// issueDelFormulario reads and checks the form; Values keeps what the user chose.
func issueDelFormulario(r *http.Request) (msg string, chosen []issueDato, values map[string]string, err error) {
	msg = r.PostFormValue("mensaje")
	values = map[string]string{"mensaje": msg}
	for _, d := range issueDatos {
		if r.PostFormValue(d.Field) != "" {
			chosen = append(chosen, d)
			values[d.Field] = "on"
		}
	}
	err = validate.Mensaje(msg)
	if err == nil && strings.TrimSpace(msg) == "" && len(chosen) == 0 {
		err = &validate.Error{Field: "mensaje", Msg: "Escribe un mensaje o elige al menos un dato para mostrar."}
	}
	return msg, chosen, values, err
}

func (s *server) vistaPreviaIssue(w http.ResponseWriter, r *http.Request) {
	msg, chosen, values, err := issueDelFormulario(r)
	data := preparacionData{Formulario: "issue"}
	status := http.StatusOK
	form := Form{Values: values, Errors: FieldErrors(err)}
	if err == nil {
		content := issueContent(msg, chosen)
		data.Issue = &issuePreview{Archivo: content, Ejemplo: renderIssue(content, exampleValues(s.state.Current(), time.Now()))}
	} else {
		status = http.StatusUnprocessableEntity
	}
	s.renderPreparacion(w, r, status, form, data)
}

func (s *server) instalarIssue(w http.ResponseWriter, r *http.Request) {
	msg, chosen, values, err := issueDelFormulario(r)
	if err != nil {
		s.renderPreparacion(w, r, http.StatusUnprocessableEntity, Form{Values: values, Errors: FieldErrors(err)}, preparacionData{Formulario: "issue"})
		return
	}
	s.runAndShow(w, r, planIssue(s.state.Current().Name, issueContent(msg, chosen)))
}

func (s *server) restaurarIssue(w http.ResponseWriter, r *http.Request) {
	s.runAndShow(w, r, planRestaurarIssue(s.state.Current().Name))
}

var (
	reloadAgetty = executor.Command{Name: "agetty", Args: []string{"--reload"}}
	showIssue    = executor.Command{Name: "sh", Args: []string{"-c", showIssueScript}}
)

// planIssue installs content as /etc/issue, makes the waiting login prompts show it
// (agetty --reload does not close open sessions, unlike restarting getty) and prints the
// result as the VM shows it.
func planIssue(target, content string) operacion.Plan {
	return operacion.Plan{
		Action:   "Cambiar mensaje de entrada",
		Target:   target,
		Commands: []executor.Command{{Name: "sh", Args: []string{"-c", writeFileScript, "sh", content, issueFile}}, reloadAgetty, showIssue},
		Success: func([]executor.Result) string {
			return "La VM ya muestra el nuevo mensaje de entrada; abajo está tal como se ve en su consola. Su mensaje original está guardado en /etc/issue.consola-original, y «Restaurar el mensaje original» lo devuelve."
		},
	}
}

func planRestaurarIssue(target string) operacion.Plan {
	restore := executor.Command{Name: "sh", Args: []string{"-c", restoreFileScript, "sh", issueFile}}
	return operacion.Plan{
		Action:   "Restaurar mensaje de entrada",
		Target:   target,
		Commands: []executor.Command{restore, reloadAgetty, showIssue},
		Success: func([]executor.Result) string {
			return "La VM volvió a su mensaje de entrada original; abajo está tal como se ve en su consola."
		},
		Explain: func(st oplog.Step) string {
			if st.Cmd == restore.String() && st.ExitCode == noOriginalCode {
				return "No hay un mensaje original guardado: la consola no ha cambiado el mensaje de esta VM desde la última vez que se restauró."
			}
			return ""
		},
	}
}
