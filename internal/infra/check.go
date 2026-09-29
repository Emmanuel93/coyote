package infra

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Finding es un hallazgo de la revisión del inventario contra el repo.
type Finding struct {
	Level string `json:"level"` // error o aviso
	Where string `json:"where"`
	Msg   string `json:"msg"`
}

// Niveles de un hallazgo.
const (
	Error = "error"
	Warn  = "aviso"
)

var (
	backendRe = regexp.MustCompile(`(?m)^\s*backend\s+"([A-Za-z0-9_]+)"`)
	cloudRe2  = regexp.MustCompile(`(?m)^\s*cloud\s*\{`)
	pinRe     = regexp.MustCompile(`(?m)^\s*(terraform|opentofu|tofu)\s+\d+\.\d+`)
)

// remoteBackends son los backends que guardan el estado fuera de la máquina.
var remoteBackends = map[string]bool{"gcs": true, "s3": true, "azurerm": true, "remote": true, "http": true, "consul": true,
	"pg": true, "kubernetes": true, "oss": true, "cos": true}

// Check compara el inventario con el repo en root. tracked son los archivos
// que git conoce (vacío si el repo no tiene commits).
func Check(root string, inv *Inventory, tracked []string) []Finding {
	var out []Finding
	add := func(level, where, format string, a ...any) {
		out = append(out, Finding{Level: level, Where: where, Msg: fmt.Sprintf(format, a...)})
	}
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil
	}
	// Versiones.
	switch {
	case inv.Versions == "":
		add(Warn, "versions", "no dice dónde se fijan las versiones de %s (por ejemplo, .tool-versions)", inv.Tool)
	case !exists(inv.Versions):
		add(Error, "versions", "%s no existe", inv.Versions)
	default:
		if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(inv.Versions))); err == nil && !pinRe.Match(data) {
			add(Warn, "versions", "%s no fija la versión de %s", inv.Versions, inv.Tool)
		}
	}
	if len(inv.Owners) == 0 {
		add(Warn, "owners", "sin dueños: nadie en particular revisa los cambios de infraestructura")
	}
	// Ambientes.
	envStacks := map[string]int{}
	for _, s := range inv.Stacks {
		envStacks[s.Env]++
	}
	for _, name := range inv.EnvNames() {
		e := inv.Environments[name]
		where := "environments." + name
		if e.VarFile != "" && !exists(e.VarFile) {
			add(Error, where, "var_file %s no existe", e.VarFile)
		}
		switch {
		case e.Budget == nil:
			add(Error, where, "sin presupuesto: declara budget.target_usd y budget.cap_usd (R13)")
		case e.Budget.CapUSD == 0:
			add(Error, where, "presupuesto pendiente: el tope mensual (cap_usd) está en 0")
		}
		if e.Apply == Local && (strings.HasPrefix(name, "prod") || name == "production") {
			add(Warn, where, "prod con apply local: conviene reviewed, que solo lo aplica la persona o un pipeline con revisor")
		}
		if len(e.Match) == 0 {
			add(Warn, where, "sin marcas (match): el gate no puede reconocer este ambiente en un comando")
		}
		if envStacks[name] == 0 {
			add(Warn, where, "ningún stack usa este ambiente")
		}
	}
	// Stacks.
	declared := map[string]bool{}
	for _, s := range inv.Stacks {
		declared[filepath.ToSlash(filepath.Clean(s.Path))] = true
		where := "stacks " + s.Path
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(s.Path)))
		if err != nil || !info.IsDir() {
			add(Error, where, "la carpeta no existe")
			continue
		}
		tf := tfFiles(root, s.Path)
		if s.Status == "scaffold" {
			if len(tf) > 0 && backendOf(root, tf) != "" {
				add(Warn, where, "está como scaffold, pero ya tiene Terraform con backend: ¿está activo?")
			}
			continue
		}
		if len(tf) == 0 {
			add(Error, where, "está activo, pero no tiene archivos .tf")
			continue
		}
		switch b := backendOf(root, tf); {
		case b == "":
			add(Error, where, "sin backend: el estado quedaría en la máquina de quien aplica")
		case !remoteBackends[b]:
			add(Error, where, "backend %q: el estado debe ser remoto (gcs, s3, azurerm…)", b)
		}
		lock := filepath.ToSlash(filepath.Join(s.Path, ".terraform.lock.hcl"))
		switch {
		case !exists(lock):
			add(Warn, where, "sin .terraform.lock.hcl: las versiones de los proveedores no quedan fijas")
		case len(tracked) > 0 && !contains(tracked, lock):
			add(Warn, where, ".terraform.lock.hcl no está en git")
		}
	}
	// Estado o carpetas .terraform en git.
	for _, f := range tracked {
		base := strings.ToLower(filepath.Base(f))
		switch {
		case strings.HasSuffix(base, ".tfstate") || strings.Contains(base, ".tfstate."):
			add(Error, f, "estado de Terraform en git: lleva secretos; sácalo y usa el backend remoto")
		case strings.Contains("/"+f+"/", "/.terraform/"):
			add(Error, f, "la carpeta .terraform está en git")
		}
	}
	// Stacks con backend que el inventario no declara.
	for _, dir := range backendDirs(root) {
		if !declared[dir] {
			add(Warn, "stacks", "%s tiene un backend y no está en el inventario", dir)
		}
	}
	return out
}

// tfFiles lista los .tf de una carpeta, sin bajar a subcarpetas.
func tfFiles(root, rel string) []string {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tf") {
			out = append(out, filepath.ToSlash(filepath.Join(rel, e.Name())))
		}
	}
	sort.Strings(out)
	return out
}

// backendOf devuelve el backend que declaran los .tf de un stack.
func backendOf(root string, files []string) string {
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil || len(data) > 2<<20 {
			continue
		}
		if m := backendRe.FindSubmatch(data); m != nil {
			return string(m[1])
		}
		if cloudRe2.Match(data) {
			return "remote"
		}
	}
	return ""
}

// backendDirs devuelve las carpetas del repo con un backend declarado.
func backendDirs(root string) []string {
	var out []string
	n := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		n++
		if n > 50000 {
			return filepath.SkipAll
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".terraform", "node_modules", ".coyote":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".tf") {
			return nil
		}
		rel, _ := filepath.Rel(root, filepath.Dir(p))
		rel = filepath.ToSlash(rel)
		if contains(out, rel) {
			return nil
		}
		if backendOf(root, []string{filepath.ToSlash(filepath.Join(rel, d.Name()))}) != "" {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
