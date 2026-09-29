package infra

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/fsx"
)

// Detect dice si un repo es de infraestructura como código: fija Terraform u
// OpenTofu en .tool-versions, o tiene archivos .tf o .tfvars.
func Detect(root string) (string, bool) {
	if data, err := fsx.ReadCapped(filepath.Join(root, ".tool-versions"), 1<<20); err == nil {
		if m := pinRe.FindSubmatch(data); m != nil {
			if string(m[1]) == "terraform" {
				return "terraform", true
			}
			return "opentofu", true
		}
	}
	tool := ""
	n := 0
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		n++
		if n > 20000 || tool != "" {
			return filepath.SkipAll
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".terraform", "node_modules", ".coyote":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".tf") || strings.HasSuffix(d.Name(), ".tfvars") {
			tool = "terraform"
		}
		return nil
	})
	return tool, tool != ""
}

var (
	makeTargetRe = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:`)
	applyRecipe  = regexp.MustCompile(`(?i)\b(?:terraform|tofu|terragrunt)\b.*\b(?:apply|destroy)\b|\bkubectl\b.*\b(?:apply|delete)\b|\bhelm\b.*\b(?:install|upgrade|uninstall)\b|\bpulumi\b.*\b(?:up|destroy)\b`)
	infraTargets = map[string]bool{"apply": true, "destroy": true, "up": true, "down": true, "deploy": true, "undeploy": true}
	knownClouds  = map[string]bool{"gcp": true, "aws": true, "azure": true, "kubernetes": true, "k8s": true, "oci": true, "digitalocean": true}
)

// Propose arma un inventario desde el repo, sin escribir nada. Lo que no se
// puede saber del repo (presupuestos y dueños) queda pendiente para la
// persona, y la revisión lo reporta.
func Propose(root string) (*Inventory, error) {
	tool, ok := Detect(root)
	if !ok {
		return nil, fmt.Errorf("no parece un repo de infraestructura como código (sin Terraform en .tool-versions ni archivos .tf)")
	}
	inv := &Inventory{Version: 1, Tool: tool, Environments: map[string]*Environment{}}
	if _, err := os.Stat(filepath.Join(root, ".tool-versions")); err == nil {
		inv.Versions = ".tool-versions"
	}
	// Ambientes: los var-files compartidos (environments/*.tfvars o envs/).
	for _, dir := range []string{"environments", "envs", "env", "vars"} {
		files, _ := filepath.Glob(filepath.Join(root, dir, "*.tfvars"))
		for _, f := range files {
			name := strings.TrimSuffix(filepath.Base(f), ".tfvars")
			if !envNameRe.MatchString(name) {
				continue
			}
			rel := dir + "/" + filepath.Base(f)
			inv.Environments[name] = &Environment{VarFile: rel, Apply: Reviewed, Match: []string{"ENV=" + name}}
		}
	}
	// Stacks: stacks/<nube>/<ambiente>, o carpetas con backend.
	for _, base := range []string{"stacks", "live"} {
		clouds, _ := os.ReadDir(filepath.Join(root, base))
		for _, c := range clouds {
			if !c.IsDir() {
				continue
			}
			envs, _ := os.ReadDir(filepath.Join(root, base, c.Name()))
			for _, e := range envs {
				if !e.IsDir() || !envNameRe.MatchString(e.Name()) {
					continue
				}
				cloud := strings.ToLower(c.Name())
				if !knownClouds[cloud] && !cloudRe.MatchString(cloud) {
					continue
				}
				rel := base + "/" + c.Name() + "/" + e.Name()
				st := Stack{Path: rel, Cloud: cloud, Env: e.Name(), Status: "scaffold"}
				if tf := tfFiles(root, rel); len(tf) > 0 {
					st.Status = "active"
				}
				inv.Stacks = append(inv.Stacks, st)
				if inv.Environments[e.Name()] == nil {
					inv.Environments[e.Name()] = &Environment{Apply: Reviewed, Match: []string{"ENV=" + e.Name()}}
				}
			}
		}
	}
	budget := int64(maxCheckBytes)
	for _, dir := range backendDirs(root, &budget) {
		known := false
		for _, s := range inv.Stacks {
			if s.Path == dir {
				known = true
			}
		}
		if !known {
			env := filepath.Base(dir)
			if inv.Environments[env] == nil {
				env = "default"
				if inv.Environments[env] == nil {
					inv.Environments[env] = &Environment{Apply: Reviewed}
				}
			}
			inv.Stacks = append(inv.Stacks, Stack{Path: dir, Cloud: "other", Env: env, Status: "active"})
		}
	}
	sort.Slice(inv.Stacks, func(i, j int) bool { return inv.Stacks[i].Path < inv.Stacks[j].Path })
	inv.Commands.Apply = makeApplyTargets(root)
	return inv, nil
}

// makeApplyTargets lee el Makefile: los targets que aplican o destruyen
// infraestructura, o que la encienden y apagan.
func makeApplyTargets(root string) []string {
	data, err := fsx.ReadCapped(filepath.Join(root, "Makefile"), 2<<20)
	if err != nil {
		return nil
	}
	var out []string
	target := ""
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSuffix(l, "\r")
		if m := makeTargetRe.FindStringSubmatch(l); m != nil && !strings.HasPrefix(l, "\t") && !strings.Contains(l, ":=") {
			target = m[1]
			if infraTargets[target] && !containsStr(out, "make "+target) {
				out = append(out, "make "+target)
			}
			continue
		}
		if strings.HasPrefix(l, "\t") && target != "" && applyRecipe.MatchString(l) && !containsStr(out, "make "+target) {
			out = append(out, "make "+target)
		}
	}
	sort.Strings(out)
	return out
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// YAML escribe el inventario con comentarios para lo que queda pendiente.
func (inv *Inventory) YAML(source string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# coyote/infra.yaml: inventario de la infraestructura (R13, ADR-0017).\n")
	if source != "" {
		fmt.Fprintf(&b, "# Propuesto por coyote desde %s: revísalo antes de llevarlo al repo.\n", source)
	}
	fmt.Fprintf(&b, "version: 1\ntool: %s\n", inv.Tool)
	if inv.Versions != "" {
		fmt.Fprintf(&b, "versions: %s\n", inv.Versions)
	}
	if len(inv.Owners) > 0 {
		fmt.Fprintf(&b, "owners: [%s]\n", quoteList(inv.Owners))
	} else {
		b.WriteString("owners: []            # pendiente: @persona o @org/equipo que revisa la infraestructura\n")
	}
	b.WriteString("environments:\n")
	for _, name := range inv.EnvNames() {
		e := inv.Environments[name]
		fmt.Fprintf(&b, "  %s:\n", name)
		if e.VarFile != "" {
			fmt.Fprintf(&b, "    var_file: %s\n", e.VarFile)
		}
		if e.Budget != nil {
			fmt.Fprintf(&b, "    budget: { target_usd: %g, cap_usd: %g }\n", e.Budget.TargetUSD, e.Budget.CapUSD)
		} else {
			b.WriteString("    budget: { target_usd: 0, cap_usd: 0 }   # pendiente: meta y tope mensual en dólares\n")
		}
		fmt.Fprintf(&b, "    apply: %s\n", e.Apply)
		if len(e.Match) > 0 {
			fmt.Fprintf(&b, "    match: [%s]\n", quoteList(e.Match))
		}
		if e.Schedule != "" {
			fmt.Fprintf(&b, "    schedule: %q\n", e.Schedule)
		}
	}
	if len(inv.Stacks) > 0 {
		b.WriteString("stacks:\n")
		for _, s := range inv.Stacks {
			fmt.Fprintf(&b, "  - { path: %s, cloud: %s, env: %s, status: %s }\n", s.Path, s.Cloud, s.Env, s.Status)
		}
	}
	if len(inv.Commands.Apply) > 0 {
		fmt.Fprintf(&b, "commands:\n  apply: [%s]\n", quoteList(inv.Commands.Apply))
	}
	return b.String()
}

func quoteList(list []string) string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(out, ", ")
}
