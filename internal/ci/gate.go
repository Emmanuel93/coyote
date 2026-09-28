package ci

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// GateInput es lo que el gate de un PR necesita para decidir (R17, D6).
type GateInput struct {
	Author    string     // quien abrió el PR
	HeadSHA   string     // último commit del PR
	Changed   []string   // archivos cambiados
	Files     []FileRisk // los de R2 o más por sus rutas
	Impact    string     // riesgo por impacto en el producto
	ImpactWhy string
	// ImpactFiles son los archivos del PR cuyas interfaces toca el cambio.
	ImpactFiles []string
	Owners      *Owners // CODEOWNERS de la rama base; nil si el repo no tiene
	Reviews     []Review
	// Member dice si una persona es de un equipo @org/equipo.
	Member func(team, user string) (bool, error)
}

// GateResult es la decisión: el riesgo, si pide revisión y si ya la tiene.
type GateResult struct {
	Risk      string   `json:"risk"`
	Why       []string `json:"why"`
	Required  bool     `json:"required"`
	OK        bool     `json:"ok"`
	Groups    []Group  `json:"groups,omitempty"`
	Approvers []string `json:"approvers,omitempty"`
	Blockers  []string `json:"blockers,omitempty"`
	Notes     []string `json:"notes,omitempty"`
}

// Group son los dueños de un conjunto de archivos: uno de ellos aprueba.
// Sin dueños, aprueba cualquier persona que no haya abierto el PR.
type Group struct {
	Owners []string `json:"owners"`
	Files  []string `json:"files"`
	OK     bool     `json:"ok"`
}

// DecideGate aplica la política: un cambio R2 o R3 pide la aprobación de un
// dueño de lo que toca; en R3 la aprobación tiene que ser del último commit.
// Quien abrió el PR no cuenta, y un dueño que pidió cambios lo detiene.
func DecideGate(in GateInput) GateResult {
	res := GateResult{Risk: R1}
	for _, f := range in.Files {
		res.Risk = Max(res.Risk, f.Risk)
	}
	if n := len(in.Files); n > 0 {
		res.Why = append(res.Why, fmt.Sprintf("%s por %s: %s", in.Files[0].Risk, pluralFiles(n), in.Files[0].Why))
	}
	if Rank(in.Impact) >= 2 {
		res.Risk = Max(res.Risk, in.Impact)
		res.Why = append(res.Why, in.Impact+" por impacto: "+in.ImpactWhy)
	}
	res.Required = Rank(res.Risk) >= 2
	if !res.Required {
		res.OK = true
		return res
	}
	author := strings.ToLower(in.Author)
	// Los grupos: por archivo riesgoso, sus dueños; si el riesgo viene solo
	// del impacto, los dueños de todo lo cambiado forman un solo grupo.
	byKey := map[string]*Group{}
	var keys []string
	seen := map[string]bool{}
	add := func(owners []string, file string) {
		if seen[file] {
			return
		}
		seen[file] = true
		k := strings.Join(owners, " ")
		g := byKey[k]
		if g == nil {
			g = &Group{Owners: owners}
			byKey[k] = g
			keys = append(keys, k)
		}
		g.Files = append(g.Files, file)
	}
	for _, f := range in.Files {
		add(sortedUnique(in.Owners.For(f.Path)), f.Path)
	}
	if Rank(in.Impact) >= 2 {
		switch {
		case len(in.ImpactFiles) > 0:
			// Los dueños de los archivos cuyas interfaces cambian.
			for _, f := range in.ImpactFiles {
				add(sortedUnique(in.Owners.For(f)), f)
			}
		case len(in.Files) == 0:
			// Sin saber qué archivo mueve el impacto, cualquiera de los dueños de lo cambiado.
			var all []string
			for _, f := range in.Changed {
				all = append(all, in.Owners.For(f)...)
			}
			owners := sortedUnique(all)
			for _, f := range in.Changed {
				add(owners, f)
			}
		}
	}
	sort.Strings(keys)
	if in.Owners == nil {
		res.Notes = append(res.Notes, "el repo no tiene CODEOWNERS: cuenta la aprobación de cualquier persona que no abrió el PR; define dueños en .github/CODEOWNERS")
	}
	unverified := map[string]bool{}
	eligible := func(g *Group, user string) bool {
		if len(g.Owners) == 0 {
			return true
		}
		for _, o := range g.Owners {
			if !strings.Contains(o, "/") {
				if strings.TrimPrefix(o, "@") == user {
					return true
				}
				continue
			}
			if in.Member == nil {
				unverified[o] = true
				continue
			}
			ok, err := in.Member(o, user)
			if errors.Is(err, ErrUnverifiable) || err != nil {
				unverified[o] = true
				continue
			}
			if ok {
				return true
			}
		}
		return false
	}
	approvers, blockers, stale := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, k := range keys {
		g := byKey[k]
		for _, r := range in.Reviews {
			if r.User == author || !eligible(g, r.User) {
				continue
			}
			switch r.State {
			case "CHANGES_REQUESTED":
				blockers[r.User] = true
			case "APPROVED":
				if res.Risk == R3 && r.CommitID != in.HeadSHA {
					stale[r.User] = true
					continue
				}
				approvers[r.User] = true
				g.OK = true
			}
		}
		res.Groups = append(res.Groups, *g)
	}
	res.Approvers, res.Blockers = keysOf(approvers), keysOf(blockers)
	for _, u := range keysOf(stale) {
		if !approvers[u] {
			res.Notes = append(res.Notes, fmt.Sprintf("la aprobación de @%s es de un commit anterior: en R3 cuenta la del último commit", u))
		}
	}
	for _, t := range keysOf(unverified) {
		res.Notes = append(res.Notes, fmt.Sprintf("no pude verificar quién es de %s con el token del job: su aprobación no cuenta hasta que el token pueda leer los equipos (Members: read) o CODEOWNERS nombre personas", t))
	}
	res.OK = len(res.Blockers) == 0
	for _, g := range res.Groups {
		res.OK = res.OK && g.OK
	}
	return res
}

func pluralFiles(n int) string {
	if n == 1 {
		return "1 archivo"
	}
	return fmt.Sprintf("%d archivos", n)
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
