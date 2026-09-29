package slo

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/fsx"
)

// File es un archivo de SLOs del proyecto.
type File struct {
	Path string // relativa al proyecto
	Spec *Spec  // nil si no valida
	Err  error
}

// maxFiles acota cuántos archivos de SLOs se leen de un proyecto y maxSpec
// lo que pesa cada uno.
const (
	maxFiles = 200
	maxSpec  = 256 << 10
)

// LoadAll lee coyote/slo/*.yaml. Sin la carpeta, no hay SLOs. Un symlink o
// un archivo que no es regular no se lee.
func LoadAll(root string) ([]File, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(Dir)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !IsSpecPath(Dir+"/"+name) {
			continue
		}
		if len(out) == maxFiles {
			return out, fmt.Errorf("%s tiene más de %d archivos; revisa la carpeta", Dir, maxFiles)
		}
		rel := Dir + "/" + name
		f := File{Path: rel}
		data, err := fsx.ReadFile(root, rel, maxSpec)
		if err != nil {
			f.Err = err
		} else if spec, err := Parse(data); err != nil {
			f.Err = err
		} else if want := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml"); spec.Service != want {
			f.Err = fmt.Errorf("service %q no coincide con el archivo (%s): el nombre del archivo es el del servicio", spec.Service, name)
		} else {
			f.Spec = spec
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// RulesPath es el archivo de reglas generado de un servicio.
func RulesPath(service string) string { return RulesDir + "/" + service + ".yaml" }

// Problem es algo que impide confiar en los SLOs de un archivo.
type Problem struct {
	Path, Msg string
}

// Stale dice si las reglas generadas de un servicio no están al día.
func Stale(root string, s *Spec) (bool, error) {
	want, err := Rules(s)
	if err != nil {
		return true, err
	}
	got, err := fsx.ReadFile(root, RulesPath(s.Service), 4*MaxFile)
	if err != nil {
		return true, nil
	}
	return !bytes.Equal(got, want), nil
}

// Check revisa los SLOs del proyecto: que validen, que su runbook exista y
// que las reglas generadas estén vigentes. También avisa de reglas generadas
// que ya no tienen su archivo de SLOs.
func Check(root string, files []File) []Problem {
	var out []Problem
	services := map[string]bool{}
	for _, f := range files {
		services[strings.TrimSuffix(path.Base(f.Path), path.Ext(f.Path))] = true
		if f.Err != nil {
			out = append(out, Problem{f.Path, f.Err.Error()})
			continue
		}
		services[f.Spec.Service] = true
		for _, o := range f.Spec.SLOs {
			rb := strings.TrimSpace(o.Alerts.Runbook)
			if rb == "" || strings.HasPrefix(rb, "https://") {
				continue
			}
			if !fsx.Regular(root, path.Clean(rb)) {
				out = append(out, Problem{f.Path, fmt.Sprintf("slo %s: el runbook %s no existe en el repo", o.Name, rb)})
			}
		}
		stale, err := Stale(root, f.Spec)
		switch {
		case err != nil:
			out = append(out, Problem{f.Path, err.Error()})
		case stale:
			out = append(out, Problem{RulesPath(f.Spec.Service), "las reglas no están al día con " + f.Path + "; corre coyote slo rules"})
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(RulesDir)))
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			switch {
			case e.IsDir() || !strings.HasSuffix(name, ".yaml"):
				// Un cargador que recorre la carpeta cargaría también esto,
				// sin que salga de ningún SLO.
				out = append(out, Problem{RulesDir + "/" + name, "en la carpeta de reglas generadas solo va <servicio>.yaml; esto no sale de ningún SLO"})
			case !services[strings.TrimSuffix(name, ".yaml")]:
				out = append(out, Problem{RulesDir + "/" + name, "reglas generadas sin su archivo de SLOs; bórralas o restaura " + Dir + "/" + name})
			}
		}
	}
	return out
}
