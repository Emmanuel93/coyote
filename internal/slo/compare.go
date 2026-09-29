package slo

import (
	"bytes"
	"fmt"
	"path"
	"reflect"
	"strings"
)

// Change es algo que un PR cambia en los SLOs de un servicio, con su riesgo.
// R3 es todo lo que puede relajar un SLO o evitar que una alerta page llegue
// a alguien; R2, lo demás (ADR-0020).
type Change struct {
	Risk string
	Why  string
}

// IsSpecPath dice si una ruta de un repo es un archivo de SLOs: *.yaml en
// coyote/slo/ del proyecto o de un proyecto en una subcarpeta, sin las reglas
// generadas.
func IsSpecPath(p string) bool {
	dir, file := path.Split(p)
	ext := path.Ext(file)
	return (dir == Dir+"/" || strings.HasSuffix(dir, "/"+Dir+"/")) && (ext == ".yaml" || ext == ".yml")
}

// Compare compara el archivo de la base con el del PR, como texto: nada se
// ejecuta. hadOld y hasNew dicen si el archivo existe en cada lado.
func Compare(oldText string, hadOld bool, newText string, hasNew bool) []Change {
	return CompareFile("", oldText, hadOld, newText, hasNew)
}

// CompareFile es Compare para un archivo con nombre: el servicio tiene que
// llamarse como el archivo.
func CompareFile(p, oldText string, hadOld bool, newText string, hasNew bool) []Change {
	name := ""
	if p != "" {
		name = strings.TrimSuffix(strings.TrimSuffix(path.Base(p), ".yaml"), ".yml")
	}
	var out []Change
	r3 := func(format string, a ...any) { out = append(out, Change{"R3", fmt.Sprintf(format, a...)}) }
	r2 := func(format string, a ...any) { out = append(out, Change{"R2", fmt.Sprintf(format, a...)}) }
	if !hasNew {
		if hadOld {
			name := "un servicio"
			if s, err := Parse([]byte(oldText)); err == nil {
				name = s.Service
			}
			r3("quita los SLOs de %s", name)
		}
		return out
	}
	head, err := Parse([]byte(newText))
	if err != nil {
		r3("el archivo de SLOs del PR no valida (%s)", shortErr(err))
		return out
	}
	if name != "" && head.Service != name {
		r3("el servicio %s no coincide con el archivo %s.yaml: sus reglas chocarían con las del servicio que sí se llama así", head.Service, name)
	}
	if !hadOld {
		r2("agrega los SLOs de %s", head.Service)
		return out
	}
	// Una base que ya no valida (por ejemplo, después de subir coyote) se
	// compara igual: arreglarla no puede esconder una relajación.
	base, err := Decode([]byte(oldText))
	if err != nil {
		r3("la base no se puede leer y no hay con qué comparar el SLO")
		return out
	}
	if base.Service != head.Service {
		r3("cambia el servicio %s por %s: los SLOs y alertas de %s dejan de existir", base.Service, head.Service, base.Service)
	}
	if base.Period != head.Period {
		r3("cambia el periodo de %s de %s a %s: cambian los umbrales de todas sus alertas", head.Service, base.Period, head.Period)
	}
	if !reflect.DeepEqual(nonNil(base.Labels), nonNil(head.Labels)) {
		r3("cambia las etiquetas de todas las reglas de %s: pueden cambiar a dónde van sus alertas", head.Service)
	}
	byName := map[string]SLO{}
	for _, o := range head.SLOs {
		byName[o.Name] = o
	}
	for _, old := range base.SLOs {
		now, ok := byName[old.Name]
		if !ok {
			r3("quita el SLO %s", old.Name)
			continue
		}
		delete(byName, old.Name)
		switch {
		case now.Objective < old.Objective:
			r3("baja el objetivo de %s de %s %% a %s %%", old.Name, num(old.Objective), num(now.Objective))
		case now.Objective > old.Objective:
			r2("sube el objetivo de %s de %s %% a %s %%", old.Name, num(old.Objective), num(now.Objective))
		}
		// Cualquier cambio de texto cuenta: un espacio dentro de un literal o
		// un salto de línea que cierra un comentario cambian lo que se mide.
		if strings.TrimSpace(old.SLI.Errors) != strings.TrimSpace(now.SLI.Errors) || strings.TrimSpace(old.SLI.Total) != strings.TrimSpace(now.SLI.Total) ||
			strings.TrimSpace(old.SLI.ErrorRatio) != strings.TrimSpace(now.SLI.ErrorRatio) {
			r3("cambia qué cuenta como error o como total en %s", old.Name)
		}
		oa, na := old.Alerts, now.Alerts
		switch {
		case !oa.Page.Disable && na.Page.Disable:
			r3("apaga la alerta page de %s", old.Name)
		case oa.Page.Disable && !na.Page.Disable:
			r2("prende la alerta page de %s", old.Name)
		}
		if !oa.Page.Disable && !na.Page.Disable && (!reflect.DeepEqual(nonNil(oa.Labels), nonNil(na.Labels)) || !reflect.DeepEqual(nonNil(oa.Page.Labels), nonNil(na.Page.Labels)) || head.AlertName(now) != base.AlertName(old)) {
			r3("cambia el nombre o las etiquetas de la alerta page de %s: puede cambiar a quién le llega", old.Name)
		}
		switch {
		case !oa.Ticket.Disable && na.Ticket.Disable:
			r2("apaga la alerta ticket de %s", old.Name)
		case !oa.Ticket.Disable && !na.Ticket.Disable && !reflect.DeepEqual(nonNil(oa.Ticket.Labels), nonNil(na.Ticket.Labels)):
			r2("cambia las etiquetas de la alerta ticket de %s", old.Name)
		}
		if strings.TrimSpace(oa.Runbook) != strings.TrimSpace(na.Runbook) {
			r2("cambia el runbook de %s", old.Name)
		}
	}
	for _, o := range head.SLOs {
		if _, added := byName[o.Name]; added {
			r2("agrega el SLO %s", o.Name)
		}
	}
	return out
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func shortErr(err error) string {
	s := strings.Join(strings.Fields(err.Error()), " ")
	if r := []rune(s); len(r) > 160 {
		return string(r[:160]) + "…"
	}
	return s
}

// InRulesDir dice si una ruta está dentro de una carpeta de reglas generadas
// (coyote/slo/prometheus/), en cualquier nivel.
func InRulesDir(p string) bool {
	return strings.HasPrefix(p, RulesDir+"/") || strings.Contains(p, "/"+RulesDir+"/")
}

// IsRulesPath dice si una ruta es el archivo de reglas generadas de un
// servicio: <servicio>.yaml directo en coyote/slo/prometheus/.
func IsRulesPath(p string) bool {
	dir, file := path.Split(p)
	return (dir == RulesDir+"/" || strings.HasSuffix(dir, "/"+RulesDir+"/")) && path.Ext(file) == ".yaml"
}

// Pair devuelve, para una ruta de SLOs o de reglas generadas, las rutas
// posibles del archivo de SLOs (.yaml y .yml) y la de sus reglas.
func Pair(p string) (specs []string, rules string) {
	dir, file := path.Split(p)
	name := strings.TrimSuffix(strings.TrimSuffix(file, ".yaml"), ".yml")
	if IsRulesPath(p) {
		base := strings.TrimSuffix(dir, "prometheus/")
		return []string{base + name + ".yaml", base + name + ".yml"}, p
	}
	return []string{p}, dir + "prometheus/" + name + ".yaml"
}

// CheckGenerated revisa, en un mismo commit, que las reglas generadas de un
// servicio salgan de su archivo de SLOs, byte por byte. Unas reglas editadas
// a mano pueden apagar una alerta sin tocar el SLO que se revisa.
func CheckGenerated(specText string, hasSpec bool, rules []byte, hasRules bool) (Change, bool) {
	switch {
	case !hasSpec && !hasRules:
		return Change{}, false
	case !hasSpec:
		return Change{"R3", "reglas de alertas sin su archivo de SLOs: no salen de ningún SLO revisado"}, true
	}
	s, err := Parse([]byte(specText))
	if err != nil {
		if hasRules {
			return Change{"R3", "las reglas generadas no se pueden comprobar: su archivo de SLOs no valida"}, true
		}
		return Change{}, false // Compare ya lo marca cuando el archivo cambia
	}
	if !hasRules {
		return Change{"R3", fmt.Sprintf("faltan las reglas generadas de %s: sus alertas no se cargan; corre coyote slo rules", s.Service)}, true
	}
	want, err := Rules(s)
	if err != nil || !bytes.Equal(want, rules) {
		return Change{"R3", fmt.Sprintf("las reglas de %s no salen de su archivo de SLOs: se editaron a mano o no se regeneraron (coyote slo rules)", s.Service)}, true
	}
	return Change{}, false
}
