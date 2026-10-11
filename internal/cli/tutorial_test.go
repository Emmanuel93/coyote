package cli

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTutorial corre cada comando de docs/tutorial/primeros-15-minutos.md en
// una carpeta nueva: el tutorial no puede quedar viejo (T58). Las líneas con
// "(no se prueba)" se saltan; <id> es la última propuesta de la cola.
func TestTutorial(t *testing.T) {
	base, _ := gateProject(t) // persona con terminal y sin variables de agente
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "tutorial", "primeros-15-minutos.md"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "tutorial")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lastID, ran := "", 0
	inBlock := false
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "```") {
			inBlock = !inBlock && strings.HasPrefix(line, "```sh")
			continue
		}
		if !inBlock {
			continue
		}
		cmd := strings.TrimSpace(line)
		if i := strings.Index(cmd, " #"); i >= 0 {
			if strings.Contains(cmd[i:], "(no se prueba)") {
				continue
			}
			cmd = strings.TrimSpace(cmd[:i])
		}
		if cmd == "" || strings.HasPrefix(cmd, "#") {
			continue
		}
		cmd = strings.ReplaceAll(cmd, "<id>", lastID)
		args := shellWords(t, cmd)
		ran++
		switch args[0] {
		case "cd":
			dir = filepath.Join(dir, args[1])
		case "git":
			c := exec.Command("git", args[1:]...)
			c.Dir = dir
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", cmd, err, out)
			}
		case "coyote":
			r := run(t, dir, "", args[1:]...)
			if r.code != 0 {
				t.Fatalf("%s salió con %d:\n%s\n%s", cmd, r.code, r.stdout, r.stderr)
			}
			if id := propRe.FindString(r.stdout + r.stderr); id != "" {
				lastID = id
			}
		default:
			t.Fatalf("el tutorial usa un comando que la prueba no sabe correr: %s", cmd)
		}
	}
	if ran < 12 {
		t.Fatalf("la prueba corrió %d comandos; ¿cambió el formato del tutorial?", ran)
	}
	if lastID == "" {
		t.Error("el tutorial debía dejar una propuesta en la cola y aprobarla")
	}
}

// shellWords separa una línea como el shell, con comillas dobles.
func shellWords(t *testing.T, s string) []string {
	t.Helper()
	var out []string
	var cur strings.Builder
	quoted, has := false, false
	for _, r := range s {
		switch {
		case r == '"':
			quoted, has = !quoted, true
		case r == ' ' && !quoted:
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if quoted {
		t.Fatalf("comillas sin cerrar en %q", s)
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}
