package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Normalized es la forma de una acción que se aprueba: lo que decide su efecto
// y nada más. Una descripción o un tiempo de espera distintos no cambian el
// hash; un carácter distinto del comando o del contenido sí.
type Normalized struct {
	Hash   string // sha256:<hex>
	Object string // descripción legible y sin secretos
	Path   string // archivo principal, relativo al proyecto si está dentro
}

// Normalize calcula el hash y la descripción de una acción.
func (ps Paths) Normalize(a Action) Normalized {
	cwd := a.Cwd
	if cwd == "" {
		cwd = ps.Root
	}
	relCwd := ps.Rel(resolve(cwd, ps.Root, ps.Home))
	var canon string
	n := Normalized{}
	switch Classify(a) {
	case KindShell:
		cmd := strings.TrimSpace(strings.NewReplacer("\\\r\n", "", "\\\n", "").Replace(a.Command))
		if a.argv != nil {
			b, _ := json.Marshal(a.argv)
			cmd = string(b)
		}
		canon = "shell\x00" + relCwd + "\x00" + cmd
		if off, _ := a.Input["dangerouslyDisableSandbox"].(bool); off {
			canon += "\x00sin-sandbox" // fuera del sandbox es otra acción
		}
		n.Object = "Bash: " + oneLine(Redact(strings.TrimSpace(a.Command)), 140)
		if relCwd != "." {
			n.Object += " (en " + relCwd + ")"
		}
	case KindFile:
		paths := targetPaths(a)
		if len(paths) > 0 {
			n.Path = ps.Rel(resolve(paths[0], cwd, ps.Home))
		}
		content, hasContent := firstString(a.Input, "content", "file_contents", "contents", "file_text")
		oldS, hasOld := a.Input["old_string"].(string)
		newS, hasNew := a.Input["new_string"].(string)
		tool := strings.ToLower(a.Tool)
		switch {
		case (tool == "write" || tool == "write_file" || tool == "create_file") && hasContent &&
			onlyKeys(a.Input, "file_path", "path", "target_file", "filePath", "content", "file_contents", "contents", "file_text"):
			canon = "write\x00" + n.Path + "\x00" + sum(content)
			n.Object = fmt.Sprintf("Write %s (%d líneas)", n.Path, lines(content))
		case tool == "edit" && hasOld && hasNew && onlyKeys(a.Input, "file_path", "old_string", "new_string", "replace_all"):
			all, _ := a.Input["replace_all"].(bool)
			canon = fmt.Sprintf("edit\x00%s\x00%s\x00%s\x00%t", n.Path, sum(oldS), sum(newS), all)
			n.Object = fmt.Sprintf("Edit %s (-%d +%d líneas)", n.Path, lines(oldS), lines(newS))
		default:
			canon = "file\x00" + a.Tool + "\x00" + canonicalJSON(relativize(a.Input, ps, cwd))
			n.Object = a.Tool + " " + n.Path
		}
	default:
		canon = "tool\x00" + a.Tool + "\x00" + canonicalJSON(a.Input)
		n.Object = a.Tool
	}
	h := sha256.Sum256([]byte(canon))
	n.Hash = "sha256:" + hex.EncodeToString(h[:])
	n.Object = strings.TrimSpace(n.Object)
	return n
}

// ShellHash calcula el hash de un comando como lo haría el gate, para aprobar
// un comando antes de que el agente lo pida.
func (ps Paths) ShellHash(cmd, cwd string) Normalized {
	return ps.Normalize(Action{Tool: "Bash", Command: cmd, Cwd: cwd, Input: map[string]any{"command": cmd}})
}

// onlyKeys informa si la entrada no trae campos fuera de los conocidos: un
// campo nuevo podría cambiar el efecto, así que entonces se hashea completa.
func onlyKeys(m map[string]any, keys ...string) bool {
	for k := range m {
		known := false
		for _, x := range keys {
			if k == x {
				known = true
				break
			}
		}
		if !known {
			return false
		}
	}
	return true
}

func firstString(m map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			return s, true
		}
	}
	return "", false
}

// relativize reemplaza en la entrada las rutas absolutas del proyecto por
// relativas, así la misma edición vale en otra copia del repo.
func relativize(in map[string]any, ps Paths, cwd string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if s, ok := v.(string); ok {
			for _, f := range pathFields {
				if k == f {
					v = ps.Rel(resolve(s, cwd, ps.Home))
				}
			}
		}
		out[k] = v
	}
	return out
}

// canonicalJSON serializa con las claves ordenadas y los números tal como
// llegaron: la misma entrada da siempre el mismo texto.
func canonicalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func lines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

func oneLine(s string, max int) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i] + " …"
	}
	r := []rune(s)
	if len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}

// secretRes reconocen credenciales comunes en un comando.
var secretRes = []*regexp.Regexp{
	regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{16,}|sk-[A-Za-z0-9_-]{16,}|xox[abposr]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{30,})\b`),
	regexp.MustCompile(`(?i)\b(bearer|token|basic)\s+[A-Za-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key)(\s*[=:]\s*)\S+`),
	regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`),
}

var longSecret = regexp.MustCompile(`[A-Za-z0-9+/_=-]{32,}`)

// Redact oculta credenciales antes de escribir una descripción en el ledger o
// en un registro versionado.
func Redact(s string) string {
	s = secretRes[0].ReplaceAllString(s, "***")
	s = secretRes[1].ReplaceAllString(s, "$1 ***")
	s = secretRes[2].ReplaceAllString(s, "$1$2***")
	s = secretRes[3].ReplaceAllString(s, "://***@")
	return longSecret.ReplaceAllStringFunc(s, func(m string) string {
		lower, upper, digit := false, false, false
		for _, r := range m {
			switch {
			case r >= 'a' && r <= 'z':
				lower = true
			case r >= 'A' && r <= 'Z':
				upper = true
			case r >= '0' && r <= '9':
				digit = true
			}
		}
		if lower && upper && digit {
			return "***"
		}
		return m
	})
}
