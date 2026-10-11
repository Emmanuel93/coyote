package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDist arma un dist con dos "binarios": copias del binario de la prueba,
// que es un binario de Go con su buildinfo.
func fakeDist(t *testing.T) string {
	t.Helper()
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	dist := t.TempDir()
	for _, p := range []string{"linux_amd64", "darwin_arm64"} {
		dir := filepath.Join(dist, "coyote_v1.0.0_"+p)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "coyote"), self, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dist
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
	dist := fakeDist(t)
	args := []string{"-dist", dist, "-version", "v1.0.0", "-commit", "0123456789abcdef0123456789abcdef01234567", "-tag", "v1.0.0", "-date", "2026-10-10T12:00:00Z"}
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
	prov := readJSON(t, filepath.Join(dist, "coyote_v1.0.0.provenance.json"))
	if prov["_type"] != "https://in-toto.io/Statement/v1" || prov["predicateType"] != "https://slsa.dev/provenance/v1" {
		t.Fatalf("procedencia: %v", prov)
	}
	if subs := prov["subject"].([]any); len(subs) != 4 {
		t.Errorf("sujetos: los dos binarios y sus dos BOM, hubo %d", len(subs))
	}
	pred := prov["predicate"].(map[string]any)
	ext := pred["buildDefinition"].(map[string]any)["externalParameters"].(map[string]any)
	if ext["ref"] != "refs/tags/v1.0.0" || ext["revision"] != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("parámetros: %v", ext)
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
	if err := run([]string{"-dist", dist, "-version", "v1.0.0", "-commit", "abc", "-date", "2026-10-10T12:00:00Z"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	prov = readJSON(t, filepath.Join(dist, "coyote_v1.0.0.provenance.json"))
	ext = prov["predicate"].(map[string]any)["buildDefinition"].(map[string]any)["externalParameters"].(map[string]any)
	if _, ok := ext["ref"]; ok {
		t.Errorf("un build sin etiqueta no lleva ref: %v", ext)
	}
	if err := run([]string{"-dist", dist}, io.Discard); err == nil {
		t.Error("sin versión, commit y fecha debe fallar")
	}
}
