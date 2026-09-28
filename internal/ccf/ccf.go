// Package ccf implementa CCF v1, el formato compacto del ledger de Coyote: una
// línea por evento, once campos separados por '|', vocabulario cerrado y
// referencias en lugar de contenido.
//
//	ts|actor|project|repo|type|scope|what|refs|tokens|cost|status
//
// La especificación completa está en docs/specs/ccf-v1.md.
package ccf

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Header es la línea de comentario que abre cada archivo del ledger.
const Header = "# ts|actor|project|repo|type|scope|what|refs|tokens in/cache/out|cost in+out|status"

// MaxWhatWords limita el campo libre para que cada evento cueste pocos tokens.
const MaxWhatWords = 12

// TSLayout es el formato de la marca de tiempo: UTC con precisión de minuto.
const TSLayout = "2006-01-02T15:04Z"

// Types es el vocabulario cerrado de tipos de evento.
var Types = map[string]string{
	"feat": "funcionalidad", "fix": "corrección", "doc": "documentación",
	"refactor": "refactor", "test": "pruebas", "chore": "mantenimiento",
	"ci": "integración continua", "build": "build", "perf": "rendimiento",
	"adr": "decisión de arquitectura", "spec": "especificación", "note": "nota de contexto",
	"idx": "indexado", "rev": "revisión", "apr": "aprobación", "rej": "rechazo",
	"gate": "gate", "attr": "atribución eliminada", "conf": "conflicto",
	"run": "step de agente", "ses": "sesión", "cost": "costo",
	"close": "cierre de workstream", "init": "inicialización", "rel": "release",
	"plan": "plan de workstream", "ask": "consulta al contexto", "sync": "sincronización con el remoto",
}

// Statuses son los estados válidos de un evento.
var Statuses = map[string]bool{"ok": true, "pend": true, "fail": true, "skip": true}

// TypeNames devuelve los tipos válidos en orden alfabético.
func TypeNames() []string {
	out := make([]string, 0, len(Types))
	for k := range Types {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Tokens son los tokens de un evento: entrada total, lecturas de caché y salida.
type Tokens struct{ In, Cache, Out int64 }

// Cost es el costo en USD separado en entrada y salida.
type Cost struct{ In, Out float64 }

// Line es un evento del ledger.
type Line struct {
	TS      time.Time
	Actor   string // @usuario, @usuario/agente o system
	Project string // workstream (W-0001) o -
	Repo    string
	Type    string
	Scope   string
	What    string
	Refs    []string // clave:valor, p. ej. sha:2f09eb6 art:A-0142
	Tokens  *Tokens
	Cost    *Cost
	Status  string
}

var (
	actorRe = regexp.MustCompile(`^(system|@[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)?)$`)
	idRe    = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}._/-]*$`)
	refRe   = regexp.MustCompile(`^[a-z][a-z0-9-]*:[^\s|]+$`)
	spaceRe = regexp.MustCompile(`\s+`)
)

// Parse interpreta y valida una línea CCF sin salto de línea final.
func Parse(s string) (Line, error) {
	f := strings.Split(s, "|")
	if len(f) != 11 {
		return Line{}, fmt.Errorf("se esperaban 11 campos y hay %d", len(f))
	}
	for i := range f {
		f[i] = strings.TrimSpace(f[i])
	}
	ts, err := ParseTS(f[0])
	if err != nil {
		return Line{}, err
	}
	l := Line{TS: ts, Actor: f[1], Project: f[2], Repo: f[3], Type: f[4], Scope: f[5], What: f[6], Status: f[10]}
	if f[7] != "-" && f[7] != "" {
		l.Refs = strings.Fields(f[7])
	}
	if f[8] != "-" {
		tk, err := ParseTokens(f[8])
		if err != nil {
			return Line{}, err
		}
		l.Tokens = &tk
	}
	if f[9] != "-" {
		c, err := ParseCost(f[9])
		if err != nil {
			return Line{}, err
		}
		l.Cost = &c
	}
	return l, l.Validate()
}

// ParseTS acepta el formato del ledger o RFC 3339.
func ParseTS(s string) (time.Time, error) {
	if t, err := time.Parse(TSLayout, s); err == nil {
		return t, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("marca de tiempo inválida %q", s)
	}
	return t.UTC(), nil
}

// Validate revisa vocabulario y límites del formato.
func (l Line) Validate() error {
	var errs []string
	if l.TS.IsZero() {
		errs = append(errs, "ts vacío")
	}
	if !actorRe.MatchString(l.Actor) {
		errs = append(errs, fmt.Sprintf("actor inválido %q (usa @usuario, @usuario/agente o system)", l.Actor))
	}
	for _, f := range []struct{ name, v string }{{"project", l.Project}, {"repo", l.Repo}, {"scope", l.Scope}} {
		if f.v != "-" && !idRe.MatchString(f.v) {
			errs = append(errs, fmt.Sprintf("%s inválido %q", f.name, f.v))
		}
	}
	if _, ok := Types[l.Type]; !ok {
		errs = append(errs, fmt.Sprintf("tipo desconocido %q (usa %s)", l.Type, strings.Join(TypeNames(), ", ")))
	}
	if strings.ContainsAny(l.What, "|\n") {
		errs = append(errs, "what no puede contener | ni saltos de línea")
	}
	if n := len(strings.Fields(l.What)); n == 0 || n > MaxWhatWords {
		errs = append(errs, fmt.Sprintf("what debe tener de 1 a %d palabras y tiene %d", MaxWhatWords, n))
	}
	for _, r := range l.Refs {
		if !refRe.MatchString(r) {
			errs = append(errs, fmt.Sprintf("referencia inválida %q (usa clave:valor)", r))
		}
	}
	if l.Tokens != nil && (l.Tokens.In < 0 || l.Tokens.Cache < 0 || l.Tokens.Out < 0 || l.Tokens.Cache > l.Tokens.In ||
		l.Tokens.In > MaxCount || l.Tokens.Out > MaxCount) {
		errs = append(errs, "tokens inválidos: fuera de rango o con más caché que entrada total")
	}
	if l.Cost != nil && !(l.Cost.In >= 0 && l.Cost.Out >= 0 && l.Cost.In <= MaxUSD && l.Cost.Out <= MaxUSD) {
		errs = append(errs, "costo inválido: negativo, no numérico o fuera de rango")
	}
	if !Statuses[l.Status] {
		errs = append(errs, fmt.Sprintf("estado inválido %q (ok, pend, fail o skip)", l.Status))
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// String serializa la línea en CCF v1.
func (l Line) String() string {
	refs := "-"
	if len(l.Refs) > 0 {
		refs = strings.Join(l.Refs, " ")
	}
	tk := "-"
	if l.Tokens != nil {
		tk = l.Tokens.String()
	}
	c := "-"
	if l.Cost != nil {
		c = l.Cost.String()
	}
	return strings.Join([]string{
		l.TS.UTC().Format(TSLayout), l.Actor, dash(l.Project), dash(l.Repo), l.Type,
		dash(l.Scope), CleanWhat(l.What), refs, tk, c, l.Status,
	}, "|")
}

// CleanWhat quita separadores y espacios sobrantes del campo libre.
func CleanWhat(s string) string {
	s = strings.ReplaceAll(s, "|", "/")
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

// ShortWhat recorta s a n palabras, con elipsis si se recortó.
func ShortWhat(s string, n int) string {
	w := strings.Fields(CleanWhat(s))
	if len(w) <= n {
		return strings.Join(w, " ")
	}
	return strings.Join(w[:n], " ") + "…"
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// String devuelve los tokens como entrada/caché/salida compactos.
func (t Tokens) String() string {
	return FormatCount(t.In) + "/" + FormatCount(t.Cache) + "/" + FormatCount(t.Out)
}

// String devuelve el costo como $entrada+$salida.
func (c Cost) String() string { return "$" + FormatUSD(c.In) + "+$" + FormatUSD(c.Out) }

// Total suma entrada y salida.
func (c Cost) Total() float64 { return c.In + c.Out }

// FormatCount compacta un conteo: 950, 12.4k, 1.2M.
func FormatCount(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return trimZero(strconv.FormatFloat(float64(n)/1000, 'f', 1, 64)) + "k"
	default:
		return trimZero(strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64)) + "M"
	}
}

// ParseCount interpreta 950, 12.4k o 1.2M.
func ParseCount(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "k"):
		mult, s = 1e3, strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "m"):
		mult, s = 1e6, strings.TrimSuffix(s, "m")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) || v*mult > MaxCount {
		return 0, fmt.Errorf("conteo inválido %q", s)
	}
	return int64(math.Round(v * mult)), nil
}

// MaxCount y MaxUSD acotan cada evento para que los totales nunca desborden.
const (
	MaxCount = 1e15
	MaxUSD   = 1e9
)

// ParseTokens interpreta entrada/caché/salida.
func ParseTokens(s string) (Tokens, error) {
	p := strings.Split(s, "/")
	if len(p) != 3 {
		return Tokens{}, fmt.Errorf("tokens inválidos %q (usa entrada/caché/salida)", s)
	}
	var v [3]int64
	for i := range p {
		n, err := ParseCount(p[i])
		if err != nil {
			return Tokens{}, err
		}
		v[i] = n
	}
	return Tokens{In: v[0], Cache: v[1], Out: v[2]}, nil
}

// ParseCost interpreta $entrada+$salida (el signo $ es opcional).
func ParseCost(s string) (Cost, error) {
	p := strings.Split(s, "+")
	if len(p) != 2 {
		return Cost{}, fmt.Errorf("costo inválido %q (usa $entrada+$salida)", s)
	}
	var v [2]float64
	for i := range p {
		f, err := strconv.ParseFloat(strings.TrimPrefix(strings.TrimSpace(p[i]), "$"), 64)
		if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) || f > MaxUSD {
			return Cost{}, fmt.Errorf("costo inválido %q", s)
		}
		v[i] = f
	}
	return Cost{In: v[0], Out: v[1]}, nil
}

// FormatUSD imprime dólares con hasta cuatro decimales sin ceros sobrantes.
func FormatUSD(v float64) string {
	s := strconv.FormatFloat(v, 'f', 4, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

func trimZero(s string) string { return strings.TrimSuffix(s, ".0") }
