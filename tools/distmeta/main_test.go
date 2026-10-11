package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildDist compila un programa mínimo, desde un repo de git propio, para
// las plataformas pedidas: binarios de Go de verdad, con su buildinfo y su
// commit. Devuelve la carpeta dist, el repo y su commit.
func buildDist(t *testing.T, platforms ...string) (dist, repo, commit string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git no está instalado")
	}
	repo = t.TempDir()
	gitc := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Ana", "-c", "user.email=ana@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/coyote\n\ngo 1.22\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	gitc("init", "-q")
	gitc("add", "-A")
	gitc("commit", "-qm", "inicio")
	commit = gitc("rev-parse", "HEAD")
	dist = filepath.Join(t.TempDir(), "dist")
	for _, p := range platforms {
		goos, goarch, _ := strings.Cut(p, "/")
		out := filepath.Join(dist, "coyote_v1.0.0_"+goos+"_"+goarch, "coyote")
		cmd := exec.Command("go", "build", "-trimpath", "-o", out, ".")
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", p, err, b)
		}
	}
	return dist, repo, commit
}

func readJSON(t *testing.T, p string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSBOMYProcedencia(t *testing.T) {
	dist, _, commit := buildDist(t, "linux/amd64", "darwin/arm64")
	args := []string{"-dist", dist, "-version", "v1.0.0", "-commit", commit, "-tag", "v1.0.0", "-date", "2026-10-10T12:00:00Z", "-platforms", "linux/amd64 darwin/arm64"}
	if err := run(args, io.Discard); err != nil {
		t.Fatal(err)
	}
	bom := readJSON(t, filepath.Join(dist, "coyote_v1.0.0_linux_amd64", "coyote.cdx.json"))
	if bom["bomFormat"] != "CycloneDX" || bom["specVersion"] != "1.7" || !strings.HasPrefix(bom["serialNumber"].(string), "urn:uuid:") {
		t.Fatalf("BOM: %v", bom)
	}
	meta := bom["metadata"].(map[string]any)
	comp := meta["component"].(map[string]any)
	if comp["name"] != "coyote" || comp["version"] != "v1.0.0" || meta["timestamp"] != "2026-10-10T12:00:00Z" {
		t.Errorf("componente: %v", comp)
	}
	props, _ := json.Marshal(comp["properties"])
	for _, want := range []string{`"GOAMD64"`, `"vcs.revision","value":"` + commit, `"vcs.modified","value":"false"`, `"-trimpath","value":"true"`} {
		if !strings.Contains(string(props), want) {
			t.Errorf("el SBOM lleva todas las opciones del build; falta %s en %s", want, props)
		}
	}
	prov := readJSON(t, filepath.Join(dist, "coyote_v1.0.0.provenance.json"))
	if prov["_type"] != "https://in-toto.io/Statement/v1" || prov["predicateType"] != "https://slsa.dev/provenance/v1" {
		t.Fatalf("procedencia: %v", prov)
	}
	if subs := prov["subject"].([]any); len(subs) != 4 {
		t.Errorf("sujetos: los dos binarios y sus dos BOM, hubo %d", len(subs))
	}
	pred := prov["predicate"].(map[string]any)
	def := pred["buildDefinition"].(map[string]any)
	ext := def["externalParameters"].(map[string]any)
	if ext["ref"] != "refs/tags/v1.0.0" || ext["revision"] != commit {
		t.Errorf("parámetros: %v", ext)
	}
	if set := def["internalParameters"].(map[string]any)["settings"].(map[string]any); set["CGO_ENABLED"] != "0" || set["GOOS"] != nil {
		t.Errorf("opciones comunes del build: %v", set)
	}
	// Las opciones propias de una arquitectura no se generalizan: GOAMD64 es
	// de amd64 y no de las demás.
	internal := def["internalParameters"].(map[string]any)
	if set := internal["settings"].(map[string]any); set["GOAMD64"] != nil {
		t.Errorf("GOAMD64 no vale para todas las plataformas: %v", set)
	}
	per := internal["platformSettings"].(map[string]any)
	if amd := per["linux_amd64"].(map[string]any); amd["GOAMD64"] == nil {
		t.Errorf("GOAMD64 va con linux_amd64: %v", per)
	}
	if arm, ok := per["darwin_arm64"].(map[string]any); ok && arm["GOAMD64"] != nil {
		t.Errorf("GOAMD64 no es de arm64: %v", arm)
	}
	if _, ok := pred["runDetails"].(map[string]any)["builder"].(map[string]any)["id"]; !ok {
		t.Error("Build L1 pide builder.id")
	}
	// Otra corrida sobre lo mismo da los mismos archivos.
	first, _ := os.ReadFile(filepath.Join(dist, "coyote_v1.0.0.provenance.json"))
	if err := run(args, io.Discard); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(filepath.Join(dist, "coyote_v1.0.0.provenance.json")); string(again) != string(first) {
		t.Error("la procedencia debe ser reproducible")
	}
	// Sin etiqueta no se inventa una ref.
	noTag := []string{"-dist", dist, "-version", "v1.0.0", "-commit", commit, "-date", "2026-10-10T12:00:00Z", "-platforms", "linux/amd64 darwin/arm64"}
	if err := run(noTag, io.Discard); err != nil {
		t.Fatal(err)
	}
	prov = readJSON(t, filepath.Join(dist, "coyote_v1.0.0.provenance.json"))
	ext = prov["predicate"].(map[string]any)["buildDefinition"].(map[string]any)["externalParameters"].(map[string]any)
	if _, ok := ext["ref"]; ok {
		t.Errorf("un build sin etiqueta no lleva ref: %v", ext)
	}
	if err := run([]string{"-dist", dist}, io.Discard); err == nil {
		t.Error("sin versión, commit, fecha y plataformas debe fallar")
	}
}

// TestSoloBinariosDelCommit: revisión adversarial de v1.0. Un binario que
// quedó en la carpeta, de otra plataforma, de otro commit o con cambios sin
// commit no entra en la procedencia.
func TestSoloBinariosDelCommit(t *testing.T) {
	dist, repo, commit := buildDist(t, "linux/amd64")
	base := []string{"-dist", dist, "-version", "v1.0.0", "-date", "2026-10-10T12:00:00Z"}
	// Un binario viejo en otra carpeta no se toma si no es una plataforma del release.
	old := filepath.Join(dist, "coyote_v0.9.0_linux_amd64")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run(append(base, "-commit", commit, "-platforms", "linux/amd64"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if subs := readJSON(t, filepath.Join(dist, "coyote_v1.0.0.provenance.json"))["subject"].([]any); len(subs) != 2 {
		t.Errorf("solo el binario de la plataforma y su BOM: %d sujetos", len(subs))
	}
	// Una carpeta de windows con un binario de linux.
	win := filepath.Join(dist, "coyote_v1.0.0_windows_amd64")
	if err := os.MkdirAll(win, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dist, "coyote_v1.0.0_linux_amd64", "coyote"))
	if err := os.WriteFile(filepath.Join(win, "coyote"), data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run(append(base, "-commit", commit, "-platforms", "linux/amd64 windows/amd64"), io.Discard); err == nil || !strings.Contains(err.Error(), "es de linux/amd64") {
		t.Errorf("binario de otra plataforma: %v", err)
	}
	if err := run(append(base, "-commit", strings.Repeat("0", 40), "-platforms", "linux/amd64"), io.Discard); err == nil || !strings.Contains(err.Error(), "sale del commit") {
		t.Errorf("binario de otro commit: %v", err)
	}
	// Con cambios sin commit, el buildinfo lo dice y no hay procedencia.
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n\nfunc main() { println() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dist, "coyote_v1.0.0_linux_amd64", "coyote"), ".")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	if err := run(append(base, "-commit", commit, "-platforms", "linux/amd64"), io.Discard); err == nil || !strings.Contains(err.Error(), "cambios sin commit") {
		t.Errorf("binario de un árbol sucio: %v", err)
	}
}

// TestOpcionesDelBuild: cuarta pasada de v1.0. Un binario compilado con
// -gcflags, -tags o -asmflags, que make dist no usa, no entra en la
// procedencia. Con -trimpath Go no registra las -ldflags: no se pueden ver.
func TestOpcionesDelBuild(t *testing.T) {
	dist, repo, commit := buildDist(t, "linux/amd64")
	base := []string{"-dist", dist, "-version", "v1.0.0", "-commit", commit, "-date", "2026-10-10T12:00:00Z", "-platforms", "linux/amd64"}
	rebuild := func(flags ...string) {
		t.Helper()
		out := filepath.Join(dist, "coyote_v1.0.0_linux_amd64", "coyote")
		cmd := exec.Command("go", append(append([]string{"build", "-trimpath"}, flags...), "-o", out, ".")...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %v: %v\n%s", flags, err, b)
		}
	}
	rebuild("-ldflags", "-s -w")
	if err := run(base, io.Discard); err != nil {
		t.Errorf("las -ldflags no se registran con -trimpath y no estorban: %v", err)
	}
	for _, flags := range [][]string{{"-gcflags", "-N"}, {"-tags", "extra"}, {"-asmflags", "-D=x"}} {
		rebuild(append([]string{"-ldflags", "-s -w"}, flags...)...)
		if err := run(base, io.Discard); err == nil || !strings.Contains(err.Error(), flags[0]) {
			t.Errorf("%s: %v", flags[0], err)
		}
	}
}
