package slo

import (
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

func norm(q string) string { return strings.Join(strings.Fields(q), " ") }

// Compare compara el archivo de la base con el del PR, como texto: nada se
// ejecuta. hadOld y hasNew dicen si el archivo existe en cada lado.
func Compare(oldText string, hadOld bool, newText string, hasNew bool) []Change {
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
	if !hadOld {
		r2("agrega los SLOs de %s", head.Service)
		return out
	}
	base, err := Parse([]byte(oldText))
	if err != nil {
		r2("corrige un archivo de SLOs que no validaba en la base")
		return out
	}
	if base.Service != head.Service {
		r3("cambia el servicio %s por %s: los SLOs y alertas de %s dejan de existir", base.Service, head.Service, base.Service)
	}
	if base.Period != head.Period {
		r2("cambia el periodo de %s de %s a %s", head.Service, base.Period, head.Period)
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
		if norm(old.SLI.Errors) != norm(now.SLI.Errors) || norm(old.SLI.Total) != norm(now.SLI.Total) || norm(old.SLI.ErrorRatio) != norm(now.SLI.ErrorRatio) {
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
	s := err.Error()
	if r := []rune(s); len(r) > 160 {
		return string(r[:160]) + "…"
	}
	return s
}
