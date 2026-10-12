package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAuditSeptimaRevision: séptima pasada adversarial de v1.0 (Q1 y Q2).
func TestAuditSeptimaRevision(t *testing.T) {
	base := setup(t)
	now := time.Now().UTC()
	past := now.AddDate(0, 0, -40).Format(time.RFC3339)
	const atrasado = "| eventos con fecha anterior a su commit | falla |"

	// Q1: la cabecera de cada commit en el git log del ledger lleva su asunto, y un
	// asunto «(from <hash>) …» en un commit de un solo padre no elige contra qué padre
	// se lo mide: sus eventos con fecha vieja se revisan igual.
	subjects := []func(root string) string{
		func(string) string { return "chore: registro" },
		func(string) string { return "(from " + strings.Repeat("1", 40) + ") chore: registro" },
		func(root string) string {
			return "(from " + strings.TrimSpace(git(t, root, "rev-parse", "HEAD")) + ") chore: registro"
		},
		func(root string) string {
			return "(from " + strings.TrimSpace(git(t, root, "rev-parse", "HEAD^{tree}")) + ") chore: registro"
		},
		func(string) string { return "(from " + strings.Repeat("a", 64) + ") chore: registro" },
	}
	for i, subject := range subjects {
		root, _ := auditNuevo(t, base, fmt.Sprintf("asunto%d", i), now)
		must(t, run(t, root, "", "record", "apr", "despliegue aprobado", "--agent", "coyote-scribe", "--ts", past), 0, "record")
		git(t, root, "add", "-A")
		s := subject(root)
		git(t, root, "commit", "-qm", s, "--no-verify")
		if r := run(t, root, "", "audit"); r.code != 1 || !strings.Contains(r.stdout, atrasado) {
			t.Errorf("asunto %q: la aprobación de agente con fecha de hace 40 días se revisa (código %d):\n%s", s, r.code, r.stdout)
		}
	}

	// Q2: con log.showRoot=false git no muestra el diff del primer commit, y lo que él
	// trajo no se revisaba. La configuración de quien audita no cambia lo que se lee.
	must(t, run(t, base, "", "init", "raiz", "--purpose", "API de pedidos de la tienda demo"), 0, "init")
	root := filepath.Join(base, "raiz")
	must(t, run(t, root, "", "record", "apr", "despliegue aprobado", "--agent", "coyote-scribe", "--ts", past), 0, "record")
	git(t, root, "add", "-A")
	auditAt(t, root, now, "commit", "-qm", "chore: adopta coyote")
	for _, cfg := range []string{"", "false"} {
		if cfg != "" {
			git(t, root, "config", "log.showRoot", cfg)
		}
		if r := run(t, root, "", "audit"); r.code != 1 || !strings.Contains(r.stdout, atrasado) {
			t.Errorf("log.showRoot=%q: lo que trae el primer commit se revisa (código %d):\n%s", cfg, r.code, r.stdout)
		}
	}
}

// firmaFalsa es el encabezado gpgsig de un commit firmado.
const firmaFalsa = "gpgsig -----BEGIN PGP SIGNATURE-----\n \n AAAA\n -----END PGP SIGNATURE-----\n"

// TestAtribucionSeptimaRevision: séptima pasada adversarial de v1.0 (Q3, Q4 y Q8).
// El historial que revisa la regla R15 se lee entero, con la configuración que tenga
// quien lo revisa, y lo que no se puede leer no da el visto bueno.
func TestAtribucionSeptimaRevision(t *testing.T) {
	base := setup(t)
	now := time.Now().UTC()
	adopta := func(name string) string {
		t.Helper()
		must(t, run(t, base, "", "init", name, "--purpose", "API de pedidos de la tienda demo"), 0, "init "+name)
		return filepath.Join(base, name)
	}
	revisa := func(root string) result {
		t.Helper()
		return run(t, root, "", "attribution", "check", "--commits", "20")
	}

	// Q4: project.yaml entra en el primer commit y log.showRoot=false: sin la base,
	// R15 no revisaba ningún commit.
	root := adopta("raiz")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "feat: adopta coyote", "-m", aiTrailer, "--no-verify")
	for _, cfg := range []string{"", "false"} {
		if cfg != "" {
			git(t, root, "config", "log.showRoot", cfg)
		}
		if r := revisa(root); r.code != 1 || !strings.Contains(r.stdout, "atribución a IA") {
			t.Errorf("log.showRoot=%q: el primer commit lleva atribución (código %d):\n%s%s", cfg, r.code, r.stdout, r.stderr)
		}
	}

	// La base es el primer commit que agregó project.yaml en la historia, no el de
	// fecha más vieja: un commit que lo devuelve con la fecha cambiada no deja fuera
	// a los que le preceden.
	root = adopta("orden")
	git(t, root, "add", "-A")
	auditAt(t, root, now.AddDate(0, 0, -10), "commit", "-qm", "feat: adopta coyote", "-m", aiTrailer, "--no-verify")
	auditAt(t, root, now.AddDate(0, 0, -9), "commit", "-q", "--allow-empty", "-m", "chore: otro", "--no-verify")
	git(t, root, "rm", "-q", "coyote/project.yaml")
	auditAt(t, root, now.AddDate(0, 0, -8), "commit", "-qm", "chore: quita", "--no-verify")
	git(t, root, "checkout", "-q", "HEAD~1", "--", "coyote/project.yaml")
	auditAt(t, root, time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), "commit", "-qm", "chore: devuelve", "--no-verify")
	if r := revisa(root); r.code != 1 || !strings.Contains(r.stdout, "atribución a IA") {
		t.Errorf("la base no depende de las fechas (código %d):\n%s%s", r.code, r.stdout, r.stderr)
	}

	// Con ramas y un reloj cambiado, el orden de las fechas no es el de la historia: la
	// adopción original la alcanza el lado que no tiene al commit que la devuelve, y el
	// log sin orden la lista antes que a ese commit. La base sigue siendo la original.
	root = adopta("ramas")
	git(t, root, "add", "-A")
	auditAt(t, root, now.AddDate(0, 0, -10), "commit", "-qm", "feat: adopta coyote", "-m", aiTrailer, "--no-verify")
	git(t, root, "checkout", "-q", "-b", "lado")
	auditAt(t, root, now.AddDate(0, 0, -8), "commit", "-q", "--allow-empty", "-m", "chore: lado", "--no-verify")
	git(t, root, "checkout", "-q", "main")
	git(t, root, "rm", "-q", "coyote/project.yaml")
	auditAt(t, root, now.AddDate(0, 0, -9), "commit", "-qm", "chore: quita", "--no-verify")
	git(t, root, "checkout", "-q", "lado~1", "--", "coyote/project.yaml")
	auditAt(t, root, time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), "commit", "-qm", "chore: devuelve", "--no-verify")
	git(t, root, "merge", "-q", "--no-commit", "--no-ff", "lado")
	write(t, root, "coyote/project.yaml", readFile(t, filepath.Join(root, "coyote", "project.yaml"))+"# nota\n")
	git(t, root, "add", "-A")
	auditAt(t, root, now.AddDate(0, 0, -1), "commit", "-qm", "chore: une el lado", "--no-verify")
	if got := git(t, root, "log", "--root", "--diff-filter=A", "--format=%s", "--", "coyote/project.yaml"); strings.Split(strings.TrimSpace(got), "\n")[0] != "feat: adopta coyote" {
		t.Fatalf("la prueba no arma una historia donde el orden por fechas y el de la historia difieren:\n%s", got)
	}
	if r := revisa(root); r.code != 1 || !strings.Contains(r.stdout, "atribución a IA") {
		t.Errorf("la base es la adopción original aunque otra rama la devuelva con otra fecha (código %d):\n%s%s", r.code, r.stdout, r.stderr)
	}

	// Q3: con log.showSignature=true, git antepone la salida de gpg a cada commit
	// firmado y el mensaje del firmado no se revisaba.
	root = adopta("firma")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "feat: adopta coyote", "--no-verify")
	gpg := filepath.Join(root, ".git", "gpg-falso")
	script := "#!/bin/sh\necho 'gpg: Firma correcta de \"Ana\"' >&2\necho '[GNUPG:] GOODSIG 0123456789ABCDEF Ana'\nexit 0\n"
	if err := os.WriteFile(gpg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "config", "gpg.program", gpg)
	git(t, root, "config", "log.showSignature", "true")
	commitCrudo(t, root, firmaFalsa, []byte("feat: firmado\n\n"+aiTrailer+"\n"))
	if sucia := git(t, root, "log", "-1", "--format=%H"); !strings.Contains(sucia, "Firma correcta") {
		t.Fatalf("la prueba no reproduce log.showSignature:\n%s", sucia)
	}
	if r := revisa(root); r.code != 1 || !strings.Contains(r.stdout, "atribución a IA") {
		t.Errorf("el commit firmado lleva atribución (código %d):\n%s%s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, root, "", "standards", "lint"); r.code == 0 || !strings.Contains(r.stdout, "R15") {
		t.Errorf("standards lint también ve el commit firmado (código %d):\n%s%s", r.code, r.stdout, r.stderr)
	}

	// Si git no puede leer el historial, tampoco se da por revisado.
	root = adopta("roto")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "feat: adopta coyote", "--no-verify")
	tree := strings.TrimSpace(git(t, root, "rev-parse", "HEAD^{tree}"))
	if err := os.Remove(filepath.Join(root, ".git", "objects", tree[:2], tree[2:])); err != nil {
		t.Fatal(err)
	}
	if r := revisa(root); r.code == 0 || strings.Contains(r.stdout, "sin atribución a herramientas de IA") {
		t.Errorf("un historial que git no lee no está limpio (código %d):\n%s%s", r.code, r.stdout, r.stderr)
	}

	// Q8: git corta el mensaje en el primer byte nulo y el trailer que sigue no
	// aparece; un commit así no se da por limpio.
	root = adopta("nulos")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "feat: adopta coyote", "--no-verify")
	sha := commitCrudo(t, root, "", []byte("feat: oculto\n\n\x00\n"+aiTrailer+"\n"))
	if visto := git(t, root, "log", "-1", "--format=%B"); strings.Contains(visto, "Co-Authored") {
		t.Fatalf("la prueba no reproduce el corte en el NUL:\n%s", visto)
	}
	r := revisa(root)
	if r.code == 0 || !strings.Contains(r.stderr+r.stdout, "bytes nulos") || !strings.Contains(r.stderr+r.stdout, sha[:7]) {
		t.Errorf("un mensaje con bytes nulos no se revisa (código %d):\n%s%s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, root, "", "standards", "lint"); r.code == 0 || !strings.Contains(r.stdout, "bytes nulos") {
		t.Errorf("standards lint tampoco da por limpio un mensaje con bytes nulos (código %d):\n%s%s", r.code, r.stdout, r.stderr)
	}
}
