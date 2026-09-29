package hub

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Conf es coyote/hub.yaml v1: lo que es de la organización y no de un
// proyecto.
type Conf struct {
	Version int    `yaml:"version"`
	Org     string `yaml:"org"`
	// Admins ven los costos de todas las personas en la web (D10).
	Admins  []string `yaml:"admins,omitempty"`
	Budgets struct {
		// MonthlyUSD es el tope mensual de la organización: la suma de sus
		// proyectos. 0 es sin tope.
		MonthlyUSD float64 `yaml:"monthly_usd,omitempty"`
	} `yaml:"budgets,omitempty"`
	Projects []Project `yaml:"projects,omitempty"`
}

// Project es un proyecto de la organización.
type Project struct {
	Name string `yaml:"name"`
	// Path es el clon local, relativo al hub o con ~. Es de la máquina de
	// cada quien: un proyecto sin clon se muestra como tal.
	Path string `yaml:"path,omitempty"`
	// Repo es owner/nombre en GitHub; informativo.
	Repo string `yaml:"repo,omitempty"`
}

var (
	orgRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	handleRe   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)
	projNameRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}._-]{0,99}$`)
	repoRe     = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)
)

// ValidOrg informa si un nombre de organización es válido.
func ValidOrg(s string) bool { return orgRe.MatchString(s) }

// MaxBudgetUSD es el tope que acepta un presupuesto: más que eso es un error
// de dedo.
const MaxBudgetUSD = 1_000_000

// Parse lee hub.yaml. Una clave desconocida es un error: un admins mal
// escrito dejaría a la organización sin admins sin que nadie lo notara.
func Parse(data []byte) (*Conf, error) {
	var c Conf
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if err := c.normalize(); err != nil {
		return nil, err
	}
	return &c, nil
}

// NormalizeHandle devuelve @persona en minúsculas, con o sin la arroba.
func NormalizeHandle(s string) (string, error) {
	h := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@"))
	if !handleRe.MatchString(h) {
		return "", fmt.Errorf("persona inválida %q: usa @usuario como en el ledger", s)
	}
	return "@" + h, nil
}

func (c *Conf) normalize() error {
	var errs []string
	if c.Version != 1 {
		errs = append(errs, fmt.Sprintf("version %d no soportada; usa version: 1", c.Version))
	}
	c.Org = strings.TrimSpace(c.Org)
	if !orgRe.MatchString(c.Org) {
		errs = append(errs, fmt.Sprintf("org inválido %q: letras, números, punto, guion o guion bajo", c.Org))
	}
	seen := map[string]bool{}
	admins := c.Admins[:0]
	for _, a := range c.Admins {
		h, err := NormalizeHandle(a)
		if err != nil {
			errs = append(errs, "admins: "+err.Error())
			continue
		}
		if !seen[h] {
			seen[h] = true
			admins = append(admins, h)
		}
	}
	c.Admins = admins
	if b := c.Budgets.MonthlyUSD; b < 0 || b > MaxBudgetUSD || math.IsNaN(b) {
		errs = append(errs, fmt.Sprintf("budgets.monthly_usd %v fuera de rango (0 a %d)", b, MaxBudgetUSD))
	}
	names := map[string]bool{}
	for i, p := range c.Projects {
		p.Name, p.Path, p.Repo = strings.TrimSpace(p.Name), strings.TrimSpace(p.Path), strings.TrimSpace(p.Repo)
		c.Projects[i] = p
		switch {
		case !projNameRe.MatchString(p.Name):
			errs = append(errs, fmt.Sprintf("projects: nombre inválido %q", p.Name))
		case names[strings.ToLower(p.Name)]:
			errs = append(errs, fmt.Sprintf("projects: %s repetido", p.Name))
		case p.Repo != "" && !repoRe.MatchString(p.Repo):
			errs = append(errs, fmt.Sprintf("projects: repo inválido %q; usa owner/nombre", p.Repo))
		case strings.ContainsAny(p.Path, "\x00\n") || strings.Contains(p.Path, "://"):
			errs = append(errs, fmt.Sprintf("projects: %s tiene una ruta inválida; va la ruta del clon local", p.Name))
		}
		names[strings.ToLower(p.Name)] = true
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// IsAdmin informa si una persona (@usuario o usuario) es admin.
func (c *Conf) IsAdmin(person string) bool {
	h, err := NormalizeHandle(person)
	if err != nil {
		return false
	}
	for _, a := range c.Admins {
		if a == h {
			return true
		}
	}
	return false
}
