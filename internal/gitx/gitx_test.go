package gitx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// aislar deja a git sin la configuración global ni del sistema de quien corre las
// pruebas: firmas, plantillas, alias y filtros ajenos no cambian lo que se prueba.
func aislar(t *testing.T) {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
}

// gitIn corre git en dir con entrada estándar y devuelve la salida.
func gitIn(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false", "-c", "user.name=Ana", "-c", "user.email=ana@example.com"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-10-01T10:00:00Z", "GIT_COMMITTER_DATE=2026-10-01T10:00:00Z")
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

func repo(t *testing.T) string {
	t.Helper()
	aislar(t)
	dir := t.TempDir()
	gitIn(t, dir, "", "init", "-q", "-b", "main")
	return dir
}

// forjar escribe un commit con los encabezados y el mensaje dados (git commit no
// deja hacer un mensaje con NUL ni un encabezado gpgsig falso) y mueve main a él.
func forjar(t *testing.T, dir, extra, message string) string {
	t.Helper()
	tree := gitIn(t, dir, "", "rev-parse", "HEAD^{tree}")
	parent := gitIn(t, dir, "", "rev-parse", "HEAD")
	obj := fmt.Sprintf("tree %s\nparent %s\nauthor Ana <ana@example.com> 1790000000 +0000\ncommitter Ana <ana@example.com> 1790000000 +0000\n%s\n%s", tree, parent, extra, message)
	sha := gitIn(t, dir, obj, "hash-object", "-t", "commit", "-w", "--stdin", "--literally")
	gitIn(t, dir, "", "update-ref", "refs/heads/main", sha)
	return sha
}

const firmaFalsa = "gpgsig -----BEGIN PGP SIGNATURE-----\n \n iQEzBAABCAAdFiEEfalsa\n =abcd\n -----END PGP SIGNATURE-----\n"

func TestLogLeeLosCamposDeCadaCommit(t *testing.T) {
	dir := repo(t)
	gitIn(t, dir, "", "commit", "-q", "--allow-empty", "-m", "feat: uno", "-m", "cuerpo del uno")
	gitIn(t, dir, "", "checkout", "-q", "-b", "lado")
	gitIn(t, dir, "", "commit", "-q", "--allow-empty", "-m", "fix: lado")
	gitIn(t, dir, "", "checkout", "-q", "main")
	gitIn(t, dir, "", "commit", "-q", "--allow-empty", "-m", "chore: main")
	gitIn(t, dir, "", "merge", "-q", "--no-ff", "-m", "merge: lado", "lado")

	all, err := Log(dir, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("4 commits con el merge, hay %d: %+v", len(all), all)
	}
	if !all[0].Merge || all[0].Subject != "merge: lado" || all[1].Merge || len(all[0].SHA) != 40 {
		t.Errorf("el merge va primero y solo él lo es: %+v", all)
	}
	last := all[3]
	if last.Subject != "feat: uno" || last.Body != "cuerpo del uno" || last.AuthorName != "Ana" || last.AuthorEmail != "ana@example.com" ||
		last.CommitterName != "Ana" || last.CommitterEmail != "ana@example.com" {
		t.Errorf("campos del primer commit: %+v", last)
	}
	sinMerges, err := Log(dir, 10, false)
	if err != nil || len(sinMerges) != 3 {
		t.Errorf("sin merges: %d commits, %v", len(sinMerges), err)
	}
	dos, err := Log(dir, 2, true)
	if err != nil || len(dos) != 2 {
		t.Errorf("-n 2: %d commits, %v", len(dos), err)
	}
	// Un rango sin commits no es un error.
	vacio, err := Log(dir, 10, true, "HEAD..HEAD")
	if err != nil || len(vacio) != 0 {
		t.Errorf("rango vacío: %v %v", vacio, err)
	}
}

// TestLogConLaFirmaMostradaPorLaConfiguracion: séptima revisión de v1.0 (Q3). Con
// log.showSignature=true git antepone la salida de gpg a cada commit firmado; el
// lector se desalineaba y el mensaje del commit firmado no se revisaba.
func TestLogConLaFirmaMostradaPorLaConfiguracion(t *testing.T) {
	dir := repo(t)
	gitIn(t, dir, "", "commit", "-q", "--allow-empty", "-m", "feat: base")
	// Un gpg que se porta bien: su salida (la que git muestra) va a stderr y el estado a stdout.
	gpg := filepath.Join(t.TempDir(), "gpg-falso")
	script := "#!/bin/sh\necho 'gpg: Firma hecha el lunes' >&2\necho 'gpg: Firma correcta de \"Ana\"' >&2\necho '[GNUPG:] GOODSIG 0123456789ABCDEF Ana'\necho '[GNUPG:] VALIDSIG 0123456789ABCDEF 2026-10-01 1790000000 0 4 0 1 8 00 0123456789ABCDEF'\nexit 0\n"
	if err := os.WriteFile(gpg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "", "config", "gpg.program", gpg)
	trailer := "Co-Authored-By: " + "Cla" + "ude <noreply@" + "anthropic.com>"
	sha := forjar(t, dir, firmaFalsa, "feat: firmado\n\n"+trailer+"\n")
	gitIn(t, dir, "", "config", "log.showSignature", "true")

	// Control: la configuración sí ensucia la salida de git log.
	if sucia := gitIn(t, dir, "", "log", "-1", "--format=%H"); !strings.Contains(sucia, "Firma correcta") {
		t.Fatalf("la prueba no reproduce log.showSignature:\n%s", sucia)
	}
	got, err := Log(dir, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SHA != sha || got[0].Subject != "feat: firmado" || got[0].Body != trailer || got[0].AuthorName != "Ana" || got[1].Subject != "feat: base" {
		t.Errorf("el commit firmado se lee entero: %+v", got)
	}
}

// TestLogConBytesNulosEnElMensaje: séptima revisión de v1.0 (Q8). git corta el
// mensaje en el primer NUL y el trailer que sigue no aparece: un commit así no se
// puede revisar y la lectura falla en vez de darlo por limpio.
func TestLogConBytesNulosEnElMensaje(t *testing.T) {
	dir := repo(t)
	gitIn(t, dir, "", "commit", "-q", "--allow-empty", "-m", "feat: base")
	trailer := "Co-Authored-By: " + "Cla" + "ude <noreply@" + "anthropic.com>"
	sha := forjar(t, dir, "", "feat: oculto\n\n\x00\n"+trailer+"\n")
	// Control: lo que git muestra no trae el trailer.
	if visto := gitIn(t, dir, "", "log", "-1", "--format=%B"); strings.Contains(visto, "Co-Authored") {
		t.Fatalf("la prueba no reproduce el corte en el NUL:\n%s", visto)
	}
	for _, n := range []int{1, 10} {
		_, err := Log(dir, n, true)
		if err == nil || !strings.Contains(err.Error(), sha[:7]) || !strings.Contains(err.Error(), "bytes nulos") {
			t.Errorf("-n %d: se esperaba el error de bytes nulos con %s, hay %v", n, sha[:7], err)
		}
	}
	// El commit con NUL fuera del rango pedido no impide leer el resto.
	if got, err := Log(dir, 10, true, "HEAD~1"); err != nil || len(got) != 1 {
		t.Errorf("sin el commit con NUL: %v %v", got, err)
	}
	// Un NUL en un encabezado no es el mensaje.
	dir2 := repo(t)
	gitIn(t, dir2, "", "commit", "-q", "--allow-empty", "-m", "feat: base")
	tree := gitIn(t, dir2, "", "rev-parse", "HEAD^{tree}")
	obj := fmt.Sprintf("tree %s\nauthor Ana <ana@example.com> 1790000000 +0000\ncommitter Ana <ana@example.com> 1790000000 +0000\nextra a\x00b\n\nfeat: encabezado raro\n", tree)
	h := gitIn(t, dir2, obj, "hash-object", "-t", "commit", "-w", "--stdin", "--literally")
	gitIn(t, dir2, "", "update-ref", "refs/heads/main", h)
	if got, err := Log(dir2, 1, true); err != nil || len(got) != 1 || got[0].Subject != "feat: encabezado raro" {
		t.Errorf("un NUL en un encabezado no es del mensaje: %v %v", got, err)
	}
}

func TestParseLogNoAceptaNadaQueNoCuadre(t *testing.T) {
	h1 := strings.Repeat("a", 40)
	h2 := strings.Repeat("b", 64) // SHA-256
	rec := func(h, parents string) string {
		return strings.Join([]string{h, parents, "Ana", "a@x", "Ana", "a@x", "asunto\n\ncuerpo\n"}, "\x00") + "\x00"
	}
	ok, err := parseLog(rec(h1, h2) + rec(h2, ""))
	if err != nil || len(ok) != 2 || ok[0].SHA != h1 || ok[1].SHA != h2 || ok[0].Subject != "asunto" || ok[0].Body != "cuerpo" {
		t.Fatalf("salida válida: %+v %v", ok, err)
	}
	if got, err := parseLog(""); err != nil || len(got) != 0 {
		t.Errorf("salida vacía: %v %v", got, err)
	}
	malas := map[string]string{
		"basura antes":             "gpg: firma\n" + rec(h1, ""),
		"basura con NUL antes":     "gpg: firma\x00" + rec(h1, ""),
		"falta el último NUL":      strings.TrimSuffix(rec(h1, ""), "\x00"),
		"un campo de más":          rec(h1, "") + "x\x00",
		"un campo de menos":        strings.Join(strings.Split(rec(h1, ""), "\x00")[:6], "\x00") + "\x00",
		"hash que no es hash":      rec("zz"+h1[2:], ""),
		"hash corto":               rec(h1[:39], ""),
		"hash largo":               rec(h1+"aaaaaaaaaa", ""),
		"hash de 65":               rec(h2+"a", ""),
		"basura al final":          rec(h1, "") + "gpg: firma",
		"hash en mayúsculas":       rec(strings.ToUpper(h1), ""),
		"el segundo no es hash":    rec(h1, "") + rec("no es un hash", ""),
		"un campo vacío de sobra":  rec(h1, "") + "\x00",
		"un registro a medias":     rec(h1, "") + h2 + "\x00" + "\x00",
		"el hash va en otro campo": strings.Join([]string{"x", h1, "Ana", "a@x", "Ana", "a@x", "m"}, "\x00") + "\x00",
	}
	for name, out := range malas {
		if got, err := parseLog(out); err == nil {
			t.Errorf("%s: se esperaba un error, hay %+v", name, got)
		}
	}
}

func TestScanNULNoAceptaNadaQueNoCuadre(t *testing.T) {
	h1, h2 := strings.Repeat("a", 40), strings.Repeat("b", 40)
	obj := func(sha, msg string) string {
		body := "tree " + strings.Repeat("c", 40) + "\nauthor A <a@x> 1 +0000\ncommitter A <a@x> 1 +0000\n\n" + msg
		return fmt.Sprintf("%s commit %d\n%s\n", sha, len(body), body)
	}
	if got, err := scanNUL([]byte(obj(h1, "uno\n")+obj(h2, "dos\n")), []string{h1, h2}); err != nil || got != "" {
		t.Errorf("limpios: %q %v", got, err)
	}
	if got, err := scanNUL([]byte(obj(h1, "uno\n")+obj(h2, "dos\x00\nCo-authored-by: x\n")), []string{h1, h2}); err != nil || got != h2 {
		t.Errorf("el segundo trae NUL: %q %v", got, err)
	}
	if got, err := scanNUL([]byte(obj(h1, "u\x00no\n")+obj(h2, "d\x00os\n")), []string{h1, h2}); err != nil || got != h1 {
		t.Errorf("el primero con NUL es el que se nombra: %q %v", got, err)
	}
	// Un mensaje que imita el encabezado de otro objeto no mueve dónde termina el primero.
	falso := "uno\n" + obj(h2, "dos\n")
	if got, err := scanNUL([]byte(obj(h1, falso)+obj(h2, "dos\n")), []string{h1, h2}); err != nil || got != "" {
		t.Errorf("encabezado falso dentro del mensaje: %q %v", got, err)
	}
	malas := map[string]struct {
		out  string
		shas []string
	}{
		"falta un objeto":      {obj(h1, "uno\n"), []string{h1, h2}},
		"sobra un objeto":      {obj(h1, "uno\n") + obj(h2, "dos\n"), []string{h1}},
		"otro hash":            {obj(h2, "uno\n"), []string{h1}},
		"no es un commit":      {strings.Replace(obj(h1, "uno\n"), " commit ", " blob ", 1), []string{h1}},
		"missing":              {h1 + " missing\n", []string{h1}},
		"tamaño de más":        {strings.Replace(obj(h1, "uno\n"), " commit ", " commit 9", 1), []string{h1}},
		"tamaño que no es":     {strings.Replace(obj(h1, "uno\n"), " commit ", " commit x", 1), []string{h1}},
		"tamaño negativo":      {h1 + " commit -1\n\n", []string{h1}},
		"sin el salto del fin": {strings.TrimSuffix(obj(h1, "uno\n"), "\n"), []string{h1}},
		"vacía":                {"", []string{h1}},
		"campos de más":        {strings.Replace(obj(h1, "uno\n"), " commit ", " commit extra ", 1), []string{h1}},
		"un campo más":         {strings.Replace(obj(h1, "uno\n"), "\ntree", " extra\ntree", 1), []string{h1}},
		"otro byte tras él":    {strings.TrimSuffix(obj(h1, "uno\n"), "\n") + "X", []string{h1}},
	}
	for name, c := range malas {
		if got, err := scanNUL([]byte(c.out), c.shas); err == nil {
			t.Errorf("%s: se esperaba un error, hay %q", name, got)
		}
	}
}
