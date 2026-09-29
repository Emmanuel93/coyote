package infra

import (
	"regexp"
	"strings"
)

// sub es un programa seguido, después de sus opciones, de un subcomando.
const sub = `\b(?:\s+\S+)*?\s+`

// applyRes aplican, destruyen o cambian el estado de la infraestructura: un
// agente nunca los corre; los corre la persona o un pipeline con revisor.
var applyRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:terraform|tofu|terragrunt)` + sub + `(?:apply|destroy|import|refresh|taint|untaint|force-unlock|run-all\s+(?:apply|destroy))\b`),
	regexp.MustCompile(`(?i)\b(?:terraform|tofu|terragrunt)` + sub + `state\s+(?:mv|rm|push|replace-provider)\b`),
	regexp.MustCompile(`(?i)\b(?:terraform|tofu)` + sub + `workspace\s+delete\b`),
	regexp.MustCompile(`(?i)\bpulumi` + sub + `(?:up|update|destroy|import|refresh|state\s+(?:delete|move|unprotect))\b`),
	regexp.MustCompile(`(?i)\b(?:cdk|cdktf)` + sub + `(?:deploy|destroy)\b`),
}

// effectRes cambian recursos de una nube o de un cluster.
var effectRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:kubectl|oc)` + sub + `(?:apply|create|delete|patch|replace|scale|rollout|drain|cordon|uncordon|edit|set|label|annotate|taint|expose|autoscale|exec|cp)\b`),
	regexp.MustCompile(`(?i)\bhelm` + sub + `(?:install|upgrade|uninstall|delete|rollback)\b`),
	regexp.MustCompile(`(?i)\bgcloud\b.*\s(?:create|delete|update|deploy|resize|set-iam-policy|add-iam-policy-binding|remove-iam-policy-binding|patch|import|restore|start|stop|reset|enable|disable)\b`),
	regexp.MustCompile(`(?i)\baws\b.*\s(?:create|delete|put|update|modify|terminate|run|attach|detach|start|stop|reboot|deregister|register)-[a-z-]+`),
	regexp.MustCompile(`(?i)\baws\b(?:\s+\S+)*?\s+s3\s+(?:rm|mv|cp|sync|rb|mb)\b`),
	regexp.MustCompile(`(?i)\baz\b.*\s(?:create|delete|update|set|start|stop|restart|deallocate|deploy|assign)\b`),
	regexp.MustCompile(`(?i)\bgsutil\b.*\s(?:rm|mv|cp|rsync|mb|rb|iam|acl)\b`),
}

// ApplyCommand dice si un comando aplica o destruye infraestructura como
// código, o cambia su estado.
func ApplyCommand(cmd string) (string, bool) {
	for _, re := range applyRes {
		if m := re.FindString(cmd); m != "" {
			return strings.Join(strings.Fields(m), " "), true
		}
	}
	return "", false
}

// Effect dice si un comando cambia recursos de una nube o de un cluster.
func Effect(cmd string) bool {
	if _, ok := ApplyCommand(cmd); ok {
		return true
	}
	for _, re := range effectRes {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

// DeclaredApply dice si el comando corre uno de los comandos de apply del
// inventario (commands.apply: make apply, make down…).
func (inv *Inventory) DeclaredApply(cmd string) (string, bool) {
	if inv == nil {
		return "", false
	}
	fields := strings.Fields(cmd)
	for _, c := range inv.Commands.Apply {
		want := strings.Fields(c)
		if len(want) == 0 {
			continue
		}
		for i := 0; i+len(want) <= len(fields); i++ {
			ok := true
			for j := range want {
				if strings.Trim(fields[i+j], `"'();&|`) != want[j] {
					ok = false
					break
				}
			}
			if ok {
				return c, true
			}
		}
	}
	return "", false
}

// EnvFor devuelve el ambiente que delata un comando por sus marcas (match):
// ENV=prod, un var-file, la carpeta de un stack o un contexto de kubectl. Si
// varios coinciden, gana el de apply reviewed.
func (inv *Inventory) EnvFor(cmd string) (string, bool) {
	if inv == nil {
		return "", false
	}
	found := ""
	for _, name := range inv.EnvNames() {
		e := inv.Environments[name]
		marks := append([]string{}, e.Match...)
		if e.VarFile != "" {
			marks = append(marks, e.VarFile)
		}
		for _, s := range inv.Stacks {
			if s.Env == name {
				marks = append(marks, s.Path)
			}
		}
		for _, m := range marks {
			if containsToken(cmd, m) {
				if found == "" || e.Apply == Reviewed {
					found = name
				}
				break
			}
		}
	}
	return found, found != ""
}

// containsToken busca tok en s como una palabra o un tramo de ruta: la marca
// "ENV=prod" no coincide con "ENV=production".
func containsToken(s, tok string) bool {
	if tok == "" {
		return false
	}
	boundary := func(r byte) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}
	for i := 0; ; {
		j := strings.Index(s[i:], tok)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(tok)
		if (start == 0 || boundary(s[start-1])) && (end == len(s) || boundary(s[end]) && s[end] != '.') {
			return true
		}
		i = start + 1
	}
}
