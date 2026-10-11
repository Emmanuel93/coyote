// distmeta escribe, para un release de coyote, el SBOM CycloneDX 1.7 de cada
// binario y la procedencia SLSA v1.2 Build L1 del conjunto (ADR-0027). Solo
// usa la biblioteca estándar y lee el buildinfo de cada binario: no baja nada.
// Todo lo que escribe sale del commit y de los binarios, así que dos corridas
// sobre el mismo commit dan los mismos archivos.
package main

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Licencias conocidas de las dependencias, por módulo.
var licenses = map[string]string{"go.yaml.in/yaml/v3": "MIT AND Apache-2.0"}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "distmeta:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	fl := flag.NewFlagSet("distmeta", flag.ContinueOnError)
	dist := fl.String("dist", "dist", "carpeta con un subdirectorio por plataforma")
	ver := fl.String("version", "", "versión del release (v1.0.0)")
	commit := fl.String("commit", "", "commit completo del que sale el release")
	tag := fl.String("tag", "", "etiqueta exacta del commit, si la tiene")
	date := fl.String("date", "", "fecha del commit, RFC 3339")
	repo := fl.String("repo", "https://github.com/Emmanuel93/coyote", "repo del código")
	goVersion := fl.String("go", "", "versión de Go del build")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *ver == "" || *commit == "" || *date == "" {
		return fmt.Errorf("faltan -version, -commit o -date")
	}
	bins, err := filepath.Glob(filepath.Join(*dist, "coyote_"+*ver+"_*", "coyote"))
	if err != nil || len(bins) == 0 {
		return fmt.Errorf("no hay binarios de %s en %s", *ver, *dist)
	}
	sort.Strings(bins)
	rel := func(p string) string { r, _ := filepath.Rel(*dist, p); return filepath.ToSlash(r) }
	var subjects []subject
	for _, bin := range bins {
		sum, err := fileSHA256(bin)
		if err != nil {
			return err
		}
		bom, err := sbom(bin, sum, *ver, *date)
		if err != nil {
			return fmt.Errorf("%s: %v", bin, err)
		}
		out := filepath.Join(filepath.Dir(bin), "coyote.cdx.json")
		if err := writeJSON(out, bom); err != nil {
			return err
		}
		bomSum, err := fileSHA256(out)
		if err != nil {
			return err
		}
		subjects = append(subjects, subject{rel(bin), map[string]string{"sha256": sum}}, subject{rel(out), map[string]string{"sha256": bomSum}})
	}
	if *goVersion == "" {
		if info, err := buildinfo.ReadFile(bins[0]); err == nil {
			*goVersion = info.GoVersion
		}
	}
	prov := provenance(subjects, *repo, *ver, *tag, *commit, *goVersion, bins)
	if err := writeJSON(filepath.Join(*dist, "coyote_"+*ver+".provenance.json"), prov); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "SBOM de %d binarios y procedencia en %s\n", len(bins), *dist)
	return nil
}

func fileSHA256(p string) (string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:]), nil
}

func writeJSON(p string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// uuidFrom arma un UUID estable a partir de un hash: el mismo binario da el
// mismo número de serie del BOM.
func uuidFrom(hexSum string) string {
	b, _ := hex.DecodeString(hexSum)
	b[6] = b[6]&0x0f | 0x50 // versión 5: derivado de un nombre
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// sbom arma el BOM CycloneDX 1.7 de un binario desde su buildinfo.
func sbom(bin, sum, ver, date string) (map[string]any, error) {
	info, err := buildinfo.ReadFile(bin)
	if err != nil {
		return nil, err
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	mainRef := "pkg:golang/" + info.Main.Path + "@" + ver
	props := []map[string]string{{"name": "go", "value": info.GoVersion}}
	for _, k := range []string{"GOOS", "GOARCH", "CGO_ENABLED", "-trimpath", "vcs.revision", "vcs.time", "vcs.modified"} {
		if v, ok := settings[k]; ok {
			props = append(props, map[string]string{"name": k, "value": v})
		}
	}
	var comps []map[string]any
	var deps []string
	for _, d := range info.Deps {
		ref := "pkg:golang/" + d.Path + "@" + d.Version
		c := map[string]any{"type": "library", "bom-ref": ref, "name": d.Path, "version": d.Version, "purl": ref}
		if d.Sum != "" {
			c["properties"] = []map[string]string{{"name": "go.sum", "value": d.Sum}}
		}
		if d.Replace != nil {
			c["pedigree"] = map[string]any{"notes": "reemplazado por " + d.Replace.Path + " (copia en el repo, ADR-0004)"}
		}
		if l, ok := licenses[d.Path]; ok {
			c["licenses"] = []map[string]string{{"expression": l}}
		}
		comps = append(comps, c)
		deps = append(deps, ref)
	}
	comps = append(comps, map[string]any{"type": "platform", "bom-ref": "go@" + info.GoVersion, "name": "go", "version": strings.TrimPrefix(info.GoVersion, "go")})
	return map[string]any{
		"bomFormat":    "CycloneDX",
		"specVersion":  "1.7",
		"serialNumber": uuidFrom(sum),
		"version":      1,
		"metadata": map[string]any{
			"timestamp": date,
			"tools":     map[string]any{"components": []map[string]any{{"type": "application", "name": "distmeta", "version": ver}}},
			"component": map[string]any{"type": "application", "bom-ref": mainRef, "name": "coyote", "version": ver,
				"purl": mainRef, "hashes": []map[string]string{{"alg": "SHA-256", "content": sum}}, "properties": props},
		},
		"components":   comps,
		"dependencies": []map[string]any{{"ref": mainRef, "dependsOn": deps}},
	}, nil
}

// provenance arma el statement in-toto v1 con la procedencia SLSA. Sin firma:
// es Build L1. Sin horas de inicio ni fin: así también es reproducible.
func provenance(subjects []subject, repo, ver, tag, commit, goVersion string, bins []string) map[string]any {
	var platforms []string
	for _, b := range bins {
		name := filepath.Base(filepath.Dir(b))
		platforms = append(platforms, strings.TrimPrefix(name, "coyote_"+ver+"_"))
	}
	// El documento del tipo de build, en el commit exacto: no cambia nunca.
	doc := repo + "/blob/" + commit + "/docs/reference/build.md"
	external := map[string]any{"source": repo, "revision": commit, "target": "make dist"}
	uri := "git+" + repo + "@" + commit
	if tag != "" {
		external["ref"] = "refs/tags/" + tag
		uri = "git+" + repo + "@refs/tags/" + tag
	}
	return map[string]any{
		"_type":         "https://in-toto.io/Statement/v1",
		"subject":       subjects,
		"predicateType": "https://slsa.dev/provenance/v1",
		"predicate": map[string]any{
			"buildDefinition": map[string]any{
				"buildType":            doc,
				"externalParameters":   external,
				"internalParameters":   map[string]any{"go": goVersion, "platforms": platforms, "flags": "-trimpath, CGO_ENABLED=0, fecha del commit"},
				"resolvedDependencies": []map[string]any{{"uri": uri, "digest": map[string]string{"gitCommit": commit}}},
			},
			"runDetails": map[string]any{
				"builder":  map[string]any{"id": doc + "#constructor-local"},
				"metadata": map[string]any{"invocationId": ver + "@" + commit},
			},
		},
	}
}
