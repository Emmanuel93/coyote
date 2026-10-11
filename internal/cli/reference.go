package cli

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// Reference devuelve la referencia de comandos en Markdown, armada desde la
// misma tabla y las mismas banderas que usa la CLI: una prueba la compara con
// docs/reference/cli.md y falla si quedó vieja.
func Reference() string {
	var b strings.Builder
	b.WriteString("# Referencia de comandos\n\n")
	b.WriteString("Generada desde la CLI: no se edita a mano. La regenera `COYOTE_UPDATE_DOCS=1 go test ./internal/cli -run TestReferenciaDeComandos`, y `make check` falla si quedó vieja.\n\n")
	b.WriteString("`coyote [-C ruta] <comando> [opciones]`. `-C` corre el comando sobre otra carpeta. Los códigos de salida están en [compatibilidad](compatibilidad.md).\n\n")
	b.WriteString("| Comando | Uso | Para qué |\n| --- | --- | --- |\n")
	for _, c := range commands {
		if c.name == "help" {
			continue
		}
		fmt.Fprintf(&b, "| [`%s`](#coyote-%s) | `%s` | %s |\n", c.name, c.name, strings.ReplaceAll(c.args, "|", "\\|"), c.summary)
	}
	for _, c := range commands {
		if c.name == "help" || c.name == "version" {
			continue
		}
		fmt.Fprintf(&b, "\n## coyote %s\n\n%s\n", c.name, c.summary)
		subs, ok := subcommands[c.name]
		if !ok {
			fmt.Fprintf(&b, "\n```\n%s\n```\n", helpText(c, []string{"-h"}))
			continue
		}
		for _, sub := range subs {
			fmt.Fprintf(&b, "\n### coyote %s %s\n\n```\n%s\n```\n", c.name, sub, helpText(c, []string{sub, "-h"}))
		}
	}
	return b.String()
}

// subcommands son los subcomandos de cada comando que los tiene, en el orden
// en que se explican.
var subcommands = map[string][]string{
	"standards": {"lint", "show", "explain", "diff"}, "hub": {"init", "status"}, "attribution": {"check", "scrub"},
	"gate": {"check", "pr"}, "secrets": {"list", "scan"}, "infra": {"check", "plan", "propose"}, "slo": {"check", "rules"},
	"get": {"context"}, "repo": {"add", "list", "fetch"}, "ws": {"check", "status", "run", "continue"}, "ci": {"impact"},
	"auth": {"login", "status", "logout"}, "generate": {"agents"}, "hooks": {"install"},
}

// helpText corre la ayuda de un comando sin tocar nada y devuelve lo que imprime.
func helpText(c *command, args []string) string {
	var out, errb bytes.Buffer
	a := &app{stdin: strings.NewReader(""), stdout: &out, stderr: &errb, rawOut: &out, rawErr: &errb,
		now: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }}
	err := c.run(a, args)
	text := strings.TrimSpace(out.String() + errb.String())
	if text == "" && err != nil {
		text = strings.TrimSpace(err.Error())
	}
	return text
}
