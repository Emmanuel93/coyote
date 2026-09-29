package infra

import (
	"path"
	"regexp"
	"strings"
)

// sub es un programa seguido, después de sus opciones, de un subcomando. El
// nombre del programa puede llevar un sufijo: el binario de un release
// (mimirtool-linux-amd64), una versión o la imagen de un contenedor
// (grafana/mimirtool:2.14.0, …@sha256:…).
const sub = `(?:[-_:@][^\s'"]*)?\b(?:\s+\S+)*?\s+`

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
	// Cargar reglas o configuración de alertas, o silenciarlas (ADR-0020). El
	// verbo cuenta en cualquier lugar después del programa: kingpin acepta
	// banderas entre el grupo y el subcomando, y @archivo trae argumentos que
	// no se ven.
	regexp.MustCompile(`(?i)\b(?:mimirtool|cortextool)` + sub + `(?:load|sync|delete)\b`),
	regexp.MustCompile(`(?i)\bamtool` + sub + `(?:add|expire|import|update)\b`),
	regexp.MustCompile(`(?i)\b(?:mimirtool|cortextool|amtool)` + sub + `@\S`),
	// promtool push manda muestras a un remote write: puede tapar un SLI.
	regexp.MustCompile(`(?i)\bpromtool` + sub + `push\b`),
	regexp.MustCompile(`(?i)\bhelmfile` + sub + `(?:apply|sync|destroy|delete)\b`),
}

// alertPathRe son las rutas de las APIs de reglas, alertas, silencios y
// remote write de Prometheus, Mimir y Alertmanager.
var alertPathRe = regexp.MustCompile(`(?i)/(?:api/v[12]/(?:silences?|alerts)|(?:prometheus/)?config/v1/rules|api/v1/rules|-/reload|api/v1/(?:push|write)|api/prom/push)(?:\b|$)`)

// httpWriteRes reconocen un cliente HTTP que escribe: curl con otro método o
// con datos, wget con --method o --post-*, y httpie (http, https, xh), que
// escribe salvo con GET.
var httpWriteRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bcurl\b.*(?:\s-X\s*["']?|\s--request(?:=|\s+)["']?)(?:POST|PUT|DELETE|PATCH)\b`),
	regexp.MustCompile(`(?i)\bcurl\b.*\s(?:-d|-F|-T|--data(?:-raw|-binary|-urlencode|-ascii)?|--form(?:-string)?|--upload-file|--json)(?:\s|=|@|["']|$|[^\s-])`),
	regexp.MustCompile(`(?i)\bwget\b.*\s(?:--method(?:=|\s+)["']?(?:POST|PUT|DELETE|PATCH)|--post-(?:data|file)|--body-(?:data|file))`),
	regexp.MustCompile(`(?i)\b(?:http|https|xh)\b(?:\s+-\S+)*\s+(?:POST|PUT|DELETE|PATCH)\b`),
}

// httpWrite dice si un comando escribe por HTTP en la API de reglas, alertas,
// silencios o remote write.
func httpWrite(cmd string) bool {
	if !alertPathRe.MatchString(cmd) {
		return false
	}
	for _, re := range httpWriteRes {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

// Programas cuyos subcomandos coyote reconoce: los que aplican
// infraestructura y los que cambian recursos o alertas.
const (
	applyProgs  = `terraform|tofu|terragrunt|pulumi|cdk|cdktf`
	effectProgs = `kubectl|oc|helm|helmfile|mimirtool|cortextool|amtool|promtool`
)

// dynamicRes reconocen un programa cuyo subcomando llega al correr: por
// xargs, por los argumentos de una función o de set -- ("$@"), por un alias
// o por una variable o sustitución en el lugar del subcomando. coyote no
// sabe qué corre (docs/specs/gate-v1.md). Con groups, también cuenta el
// verbo de un grupo (amtool silence ${x:-add}); una variable en otro
// argumento (kubectl logs -n "$NS") no cambia qué hace el comando.
func dynamicRes(progs string, groups bool) []*regexp.Regexp {
	// El programa como palabra entera: terraform.tfvars o notas-terraform.md
	// no son el programa.
	name := `(?:^|[\s/=])(?:` + progs + `)(?:[-_:@][^\s'"]*)?`
	out := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bxargs\b[^;&|\n]*?\s(?:[\w./-]*/)?(?:` + progs + `)(?:[-_:@][^\s'"]*)?(?:\s|$)`),
		regexp.MustCompile(`(?i)` + name + `(?:\s[^;&|\n]*?)?\$(?:[@*#]|[0-9]|\{[@*#0-9])`),
		regexp.MustCompile(`(?i)\balias\s+[^=\s]+=["']?(?:[\w./-]*/)?(?:` + progs + `)(?:[-_:@][^\s'"]*)?(?:\s|$|["';])`),
		regexp.MustCompile(`(?i)` + name + `(?:\s+-\S+)*\s+["']?(?:\$|` + "`" + `)`),
	}
	if groups {
		// El verbo en la segunda palabra: amtool silence ${x:-add},
		// mimirtool rules $v, kubectl rollout $v.
		out = append(out, regexp.MustCompile(`(?i)`+name+`(?:\s+-\S+)*\s+(?:silence|silences|rules|alertmanager|alert|config|rollout|plugin)(?:\s+-\S+)*\s+["']?(?:\$|`+"`"+`)`))
	}
	return out
}

var (
	dynamicApplyRes  = dynamicRes(applyProgs, false)
	dynamicEffectRes = dynamicRes(effectProgs, true)
)

// DynamicApply dice si un comando corre una herramienta de infraestructura
// como código con un subcomando que coyote no puede leer: podría ser apply.
func DynamicApply(cmd string) (string, bool) {
	for _, re := range dynamicApplyRes {
		if m := re.FindString(cmd); m != "" {
			return strings.Join(strings.Fields(m), " "), true
		}
	}
	return "", false
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

// Effect dice si un comando cambia recursos de una nube o de un cluster, o
// las reglas y alertas de un ambiente. Uno con un subcomando que llega al
// correr cuenta como efecto.
func Effect(cmd string) bool {
	if _, ok := ApplyCommand(cmd); ok {
		return true
	}
	if _, ok := DynamicApply(cmd); ok {
		return true
	}
	for _, re := range effectRes {
		if re.MatchString(cmd) {
			return true
		}
	}
	for _, re := range dynamicEffectRes {
		if re.MatchString(cmd) {
			return true
		}
	}
	return httpWrite(cmd)
}

// DeclaredApply dice si un segmento de un comando, palabra por palabra y sin
// comillas, corre uno de los comandos de apply del inventario
// (commands.apply: make apply, make down…). Cuenta el programa por su nombre
// (/usr/bin/make, gmake no) en cualquier lugar del segmento, con los targets
// entre sus argumentos: make -C . apply, make ENV=prod apply y make -j4
// destroy corren el target igual.
func (inv *Inventory) DeclaredApply(words []string) (string, bool) {
	if inv == nil {
		return "", false
	}
	for _, c := range inv.Commands.Apply {
		want := strings.Fields(c)
		if len(want) == 0 {
			continue
		}
		for i, w := range words {
			if !strings.EqualFold(path.Base(w), want[0]) {
				continue
			}
			rest := words[i+1:]
			all := true
			for _, t := range want[1:] {
				if !containsWord(rest, t) {
					all = false
					break
				}
			}
			if all {
				return c, true
			}
		}
	}
	return "", false
}

func containsWord(list []string, w string) bool {
	for _, x := range list {
		if x == w {
			return true
		}
	}
	return false
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
