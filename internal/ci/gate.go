package ci

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/secrets"
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
	// Excluded son quienes escribieron commits del PR: no revisan su propio código.
	Excluded []string
	// Member dice si una persona es de un equipo @org/equipo.
	Member func(team, user string) (bool, error)
	// Secrets son los secretos que el cambio agrega (R18): el PR no pasa con
	// ellos, aunque lo apruebe un dueño.
	Secrets []secrets.Finding
	// PlanRisk y PlanWhy vienen del plan de Terraform del PR (coyote infra
	// plan, ADR-0017), si el pipeline lo generó.
	PlanRisk string
	PlanWhy  string
	// Leaks son los hallazgos de gitleaks en los commits del PR (ADR-0021);
	// LeaksTotal cuenta también los que no se guardaron.
	Leaks      []Leak
	LeaksTotal int
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
		// Los motivos, del más riesgoso al menos, con cuántos archivos cada uno.
		type reason struct{ risk, why string }
		counts := map[reason]int{}
		var order []reason
		for _, f := range in.Files {
			k := reason{f.Risk, f.Why}
			if counts[k] == 0 {
				order = append(order, k)
			}
			counts[k]++
		}
		sort.SliceStable(order, func(i, j int) bool {
			if Rank(order[i].risk) != Rank(order[j].risk) {
				return Rank(order[i].risk) > Rank(order[j].risk)
			}
			return counts[order[i]] > counts[order[j]]
		})
		parts := make([]string, len(order))
		for i, k := range order {
			if k.risk == res.Risk {
				parts[i] = fmt.Sprintf("%s (%d)", k.why, counts[k])
			} else {
				parts[i] = fmt.Sprintf("%s (%d, %s)", k.why, counts[k], k.risk)
			}
		}
		res.Why = append(res.Why, fmt.Sprintf("%s por %s: %s", res.Risk, pluralFiles(n), strings.Join(parts, ", ")))
	}
	if Rank(in.Impact) >= 2 {
		res.Risk = Max(res.Risk, in.Impact)
		res.Why = append(res.Why, in.Impact+" por impacto: "+in.ImpactWhy)
	}
	if Rank(in.PlanRisk) >= 2 {
		res.Risk = Max(res.Risk, in.PlanRisk)
		res.Why = append(res.Why, in.PlanRisk+" por el plan de Terraform: "+in.PlanWhy)
	}
	if n := len(in.Secrets); n > 0 {
		res.Risk = R3
		res.Why = append(res.Why, fmt.Sprintf("R3 por %s en el cambio (R18)", pluralWord(n, "secreto", "secretos")))
	}
	if n := max(in.LeaksTotal, len(in.Leaks)); n > 0 {
		res.Risk = R3
		res.Why = append(res.Why, fmt.Sprintf("R3 por %s de gitleaks en los commits del PR", pluralWord(n, "hallazgo", "hallazgos")))
	}
	res.Required = Rank(res.Risk) >= 2
	if !res.Required {
		res.OK = true
		return res
	}
	author := strings.ToLower(in.Author)
	excluded := map[string]bool{}
	for _, u := range in.Excluded {
		excluded[strings.ToLower(u)] = true
	}
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
	// El riesgo del plan de Terraform es del cambio entero: lo aprueba un
	// dueño de lo cambiado. Y un riesgo que pide revisión nunca queda sin
	// grupo: sin grupos, el PR pasaría sin ninguna aprobación.
	if Rank(in.PlanRisk) >= 2 || len(keys) == 0 {
		var all []string
		for _, f := range in.Changed {
			all = append(all, in.Owners.For(f)...)
		}
		owners := sortedUnique(all)
		files := in.Changed
		if len(files) == 0 {
			files = []string{"(el cambio)"}
		}
		// Una clave propia: no se mezcla con un grupo de archivos riesgosos.
		k := "plan " + strings.Join(owners, " ")
		if byKey[k] == nil {
			byKey[k] = &Group{Owners: owners, Files: files}
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if in.Owners == nil {
		res.Notes = append(res.Notes, "el repo no tiene CODEOWNERS: cuenta la aprobación de cualquier persona que no abrió el PR; define dueños en .github/CODEOWNERS")
	}
	unverified := map[string]bool{}
	// eligible dice si una persona es dueña del grupo; unsure, si no se puede saber.
	eligible := func(g *Group, user string) (ok, unsure bool) {
		if len(g.Owners) == 0 {
			return true, false
		}
		for _, o := range g.Owners {
			switch {
			case !strings.HasPrefix(o, "@"):
				// Un correo no se compara con un login de GitHub.
				unverified[o] = true
				unsure = true
			case !strings.Contains(o, "/"):
				if strings.TrimPrefix(o, "@") == user {
					return true, false
				}
			case in.Member == nil:
				unverified[o] = true
				unsure = true
			default:
				member, err := in.Member(o, user)
				if err != nil {
					unverified[o] = true
					unsure = true
					continue
				}
				if member {
					return true, false
				}
			}
		}
		return false, unsure
	}
	approvers, blockers, stale := map[string]bool{}, map[string]bool{}, map[string]bool{}
	unsureBlock := map[string]bool{}
	for _, k := range keys {
		g := byKey[k]
		for _, r := range in.Reviews {
			if r.User == author || excluded[r.User] {
				continue
			}
			ok, unsure := eligible(g, r.User)
			switch {
			case r.State == "CHANGES_REQUESTED" && (ok || unsure):
				// Un pedido de cambios de quien puede ser dueño detiene el merge.
				blockers[r.User] = true
				if !ok {
					unsureBlock[r.User] = true
				}
			case r.State == "APPROVED" && ok:
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
		if strings.HasPrefix(t, "@") {
			res.Notes = append(res.Notes, fmt.Sprintf("no pude verificar quién es de %s con el token del job: su aprobación no cuenta hasta que el token pueda leer los equipos (Members: read) o CODEOWNERS nombre personas", t))
		} else {
			res.Notes = append(res.Notes, fmt.Sprintf("%s es dueño por correo: coyote no lo puede comparar con quien aprueba en GitHub; nombra @persona o @org/equipo en CODEOWNERS", t))
		}
	}
	for _, u := range keysOf(unsureBlock) {
		res.Notes = append(res.Notes, fmt.Sprintf("@%s pidió cambios y no pude verificar si es dueño: su pedido cuenta hasta que lo resuelva", u))
	}
	res.OK = len(res.Blockers) == 0 && len(in.Secrets) == 0
	for _, g := range res.Groups {
		res.OK = res.OK && g.OK
	}
	if len(in.Secrets) > 0 {
		res.Notes = append(res.Notes, "el cambio agrega secretos: ninguna aprobación lo deja pasar; sácalos del PR y rótalos, porque ya están en GitHub")
	}
	return res
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
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
