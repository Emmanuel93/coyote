package slo

import (
	"bytes"
	"fmt"
	"path"
	"reflect"
	"sort"
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
// coyote/slo/ del proyecto o de un proyecto en una subcarpeta. Nada dentro de
// una carpeta de reglas generadas es un archivo de SLOs.
func IsSpecPath(p string) bool {
	dir, file := path.Split(p)
	ext := path.Ext(file)
	return !InRulesDir(p) && (dir == Dir+"/" || strings.HasSuffix(dir, "/"+Dir+"/")) && (ext == ".yaml" || ext == ".yml") && goodStem(file)
}

// goodStem dice si un archivo tiene nombre antes de la extensión y no es
// oculto: .yaml no es el SLO de ningún servicio.
func goodStem(file string) bool {
	stem := strings.TrimSuffix(strings.TrimSuffix(file, ".yaml"), ".yml")
	return stem != "" && !strings.HasPrefix(stem, ".")
}

// ServiceOf es el servicio de un archivo de SLOs o de reglas: su nombre sin
// extensión.
func ServiceOf(p string) string {
	return strings.TrimSuffix(strings.TrimSuffix(path.Base(p), ".yaml"), ".yml")
}

// Duplicates agrupa archivos de SLOs por servicio y devuelve los servicios
// declarados en más de un archivo (x.yaml y x.yml, o en otra carpeta): sus
// reglas se llaman igual y, al cargarlas, unas pisan a las otras.
func Duplicates(specs []string) map[string][]string {
	by := map[string][]string{}
	for _, p := range specs {
		by[ServiceOf(p)] = append(by[ServiceOf(p)], p)
	}
	out := map[string][]string{}
	for svc, ps := range by {
		if len(ps) > 1 {
			sort.Strings(ps)
			out[svc] = ps
		}
	}
	return out
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
		name = ServiceOf(p)
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
		// Sin page, el ticket es la única alerta: cambiar a quién le llega pesa igual.
		onlyTicket := na.Page.Disable && !oa.Ticket.Disable && !na.Ticket.Disable
		ticketRoute := !reflect.DeepEqual(nonNil(oa.Ticket.Labels), nonNil(na.Ticket.Labels)) ||
			!reflect.DeepEqual(nonNil(oa.Labels), nonNil(na.Labels)) || head.AlertName(now) != base.AlertName(old)
		switch {
		case !oa.Ticket.Disable && na.Ticket.Disable:
			// Con la page apagada, sin ticket el SLO se queda sin ninguna alerta.
			r3("apaga la alerta ticket de %s", old.Name)
		case onlyTicket && ticketRoute:
			r3("cambia el nombre o las etiquetas de la alerta ticket de %s, su única alerta: puede cambiar a quién le llega", old.Name)
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

// rulesDirEnd devuelve dónde termina la primera carpeta de reglas generadas
// (coyote/slo/prometheus/) de una ruta, o -1.
func rulesDirEnd(p string) int {
	if strings.HasPrefix(p, RulesDir+"/") {
		return len(RulesDir) + 1
	}
	if i := strings.Index(p, "/"+RulesDir+"/"); i >= 0 {
		return i + len(RulesDir) + 2
	}
	return -1
}

// InRulesDir dice si una ruta está dentro de una carpeta de reglas generadas
// (coyote/slo/prometheus/), en cualquier nivel.
func InRulesDir(p string) bool { return rulesDirEnd(p) >= 0 }

// IsRulesPath dice si una ruta es el archivo de reglas generadas de un
// servicio: <servicio>.yaml directo en la primera carpeta de reglas de la
// ruta. Lo que está más abajo, aunque parezca otro proyecto, es un archivo
// suelto en la carpeta de reglas.
func IsRulesPath(p string) bool {
	end := rulesDirEnd(p)
	if end < 0 {
		return false
	}
	rest := p[end:]
	return rest != "" && !strings.Contains(rest, "/") && path.Ext(rest) == ".yaml" && goodStem(rest)
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
	if !hasRules {
		// Aunque el SLO ya no valide (por ejemplo, después de subir coyote),
		// borrar sus reglas apaga todas sus alertas.
		name := "un servicio"
		if err == nil {
			name = s.Service
		}
		return Change{"R3", fmt.Sprintf("faltan las reglas generadas de %s: sus alertas no se cargan; corre coyote slo rules", name)}, true
	}
	if err != nil {
		return Change{"R3", "las reglas generadas no se pueden comprobar: su archivo de SLOs no valida"}, true
	}
	want, err := Rules(s)
	if err != nil || !bytes.Equal(want, rules) {
		return Change{"R3", fmt.Sprintf("las reglas de %s no salen de su archivo de SLOs: se editaron a mano o no se regeneraron (coyote slo rules)", s.Service)}, true
	}
	return Change{}, false
}
