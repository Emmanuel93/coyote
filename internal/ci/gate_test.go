package ci

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Emmanuel93/coyote/internal/github"
)

func TestRiesgoPorRutas(t *testing.T) {
	files := []string{"README.md", "src/main/java/demo/Pedidos.java", "services/pagos/src/main/resources/application-prod.yml",
		"db/migrations/V3__cobros.sql", ".github/workflows/ci.yml", "api/openapi.yaml", "app/pubspec.yaml", "deploy/Dockerfile",
		"src/auth/login.go", "author.go"}
	rs := PathRisks(files, DefaultRules)
	got := map[string]string{}
	for _, r := range rs {
		got[r.Path] = r.Risk
	}
	want := map[string]string{"services/pagos/src/main/resources/application-prod.yml": R2, "db/migrations/V3__cobros.sql": R3,
		".github/workflows/ci.yml": R3, "api/openapi.yaml": R2, "app/pubspec.yaml": R2, "deploy/Dockerfile": R3, "src/auth/login.go": R3}
	if len(got) != len(want) {
		t.Errorf("riesgos: %v", got)
	}
	for f, r := range want {
		if got[f] != r {
			t.Errorf("%s: %q, se esperaba %s", f, got[f], r)
		}
	}
	if rs[0].Risk != R3 || rs[len(rs)-1].Risk != R2 {
		t.Errorf("orden por riesgo: %v", rs)
	}
	// Una regla del proyecto sube el riesgo de sus rutas.
	rule, err := ParseRiskFlag("R3=services/*/src/**/cobro*/**")
	if err != nil {
		t.Fatal(err)
	}
	rs = PathRisks([]string{"services/pagos/src/main/java/cobros/Cobro.java"}, append(DefaultRules, rule))
	if len(rs) != 1 || rs[0].Risk != R3 || !strings.Contains(rs[0].Why, "regla del proyecto") {
		t.Errorf("regla del proyecto: %v", rs)
	}
	for _, bad := range []string{"R4=x", "R3", "R3=", "R2=!x", "R3=../x", "R3=a b"} {
		if _, err := ParseRiskFlag(bad); err == nil {
			t.Errorf("%q debería fallar", bad)
		}
	}
	if Max(R1, R3) != R3 || Max(R2, R1) != R2 || Max("", R1) != R1 {
		t.Error("Max")
	}
}

func TestCodeowners(t *testing.T) {
	o := ParseCodeowners(`# dueños
*                    @Ana
*.sql                @org/datos
/docs/               @luis
apps/                @org/apps
/services/pagos      @org/Pagos @marta
/services/pagos/README.md
docs/*.md            @luis @eva   # comentario
!ignorado            @nadie
`)
	cases := map[string]string{
		"main.go":                            "@ana",
		"db/V1.sql":                          "@org/datos",
		"docs/guia.md":                       "@eva @luis",
		"docs/sub/x.png":                     "@luis",
		"a/apps/b.ts":                        "@org/apps",
		"services/pagos/src/Cobro.java":      "@marta @org/pagos",
		"services/pagos/README.md":           "",
		"services/pagos-viejo/x.java":        "@ana",
		"otro/services/pagos/src/Cobro.java": "@ana",
	}
	for path, want := range cases {
		got := sortedUnique(o.For(path))
		if strings.Join(got, " ") != want {
			t.Errorf("%s: %v, se esperaba %q", path, got, want)
		}
	}
	var nilOwners *Owners
	if nilOwners.For("x") != nil {
		t.Error("sin CODEOWNERS no hay dueños")
	}
}

func TestDecideGate(t *testing.T) {
	head := strings.Repeat("h", 40)
	owners := ParseCodeowners("* @ana\n/pagos/ @org/pagos\n")
	member := func(team, user string) (bool, error) {
		if team == "@org/secreto" {
			return false, ErrUnverifiable
		}
		return team == "@org/pagos" && user == "marta", nil
	}
	base := GateInput{Author: "Beto", HeadSHA: head, Owners: owners, Member: member,
		Changed: []string{"pagos/application.yml", "README.md"},
		Files:   []FileRisk{{Path: "pagos/application.yml", Risk: R2, Why: "la configuración del servicio"}}}

	// El resumen agrupa los motivos con cuántos archivos cada uno.
	sum := DecideGate(GateInput{Files: []FileRisk{{"a.sql", R3, "el esquema de datos"}, {"b.sql", R3, "el esquema de datos"}, {"Dockerfile", R3, "infraestructura"}, {"go.mod", R2, "las dependencias"}}})
	if len(sum.Why) != 1 || sum.Why[0] != "R3 por 4 archivos: el esquema de datos (2), infraestructura (1), las dependencias (1, R2)" {
		t.Errorf("resumen: %q", sum.Why)
	}
	// R1: nada que revisar.
	r := DecideGate(GateInput{Changed: []string{"README.md"}})
	if r.Required || !r.OK || r.Risk != R1 {
		t.Errorf("R1: %+v", r)
	}
	// R2 sin revisiones: espera a un dueño del archivo riesgoso.
	r = DecideGate(base)
	if !r.Required || r.OK || r.Risk != R2 || len(r.Groups) != 1 || strings.Join(r.Groups[0].Owners, " ") != "@org/pagos" {
		t.Errorf("R2 pendiente: %+v", r)
	}
	// Aprueba quien no es dueño, y el autor tampoco cuenta.
	in := base
	in.Reviews = []Review{{User: "ana", State: "APPROVED", CommitID: head}, {User: "beto", State: "APPROVED", CommitID: head}}
	if r = DecideGate(in); r.OK {
		t.Errorf("solo cuenta un dueño del archivo: %+v", r)
	}
	// Aprueba un miembro del equipo dueño.
	in.Reviews = append(in.Reviews, Review{User: "marta", State: "APPROVED", CommitID: "viejo"})
	if r = DecideGate(in); !r.OK || strings.Join(r.Approvers, ",") != "marta" {
		t.Errorf("aprobado por el equipo (en R2 vale un commit anterior): %+v", r)
	}
	// R3 por impacto: la aprobación tiene que ser del último commit.
	in.Impact, in.ImpactWhy = R3, "rompe 1 interfaz"
	r = DecideGate(in)
	if r.Risk != R3 || r.OK || !strings.Contains(strings.Join(r.Notes, "\n"), "@marta es de un commit anterior") {
		t.Errorf("R3 con aprobación vieja: %+v", r)
	}
	in.Reviews[2].CommitID = head
	if r = DecideGate(in); !r.OK || len(r.Why) != 2 {
		t.Errorf("R3 aprobado en el último commit: %+v", r)
	}
	// Un dueño que pide cambios detiene el merge.
	in.Reviews = append(in.Reviews, Review{User: "marta2", State: "CHANGES_REQUESTED"})
	member2 := func(team, user string) (bool, error) { return user == "marta" || user == "marta2", nil }
	in.Member = member2
	if r = DecideGate(in); r.OK || strings.Join(r.Blockers, ",") != "marta2" {
		t.Errorf("cambios pedidos: %+v", r)
	}
	// Un equipo que el token no ve no cuenta y lo dice.
	in = base
	in.Owners = ParseCodeowners("* @org/secreto\n")
	in.Reviews = []Review{{User: "zoe", State: "APPROVED", CommitID: head}}
	if r = DecideGate(in); r.OK || !strings.Contains(strings.Join(r.Notes, "\n"), "no pude verificar quién es de @org/secreto") {
		t.Errorf("equipo sin verificar: %+v", r)
	}
	// Quien escribió commits del PR no aprueba su propio código.
	in = base
	in.Reviews = []Review{{User: "marta", State: "APPROVED", CommitID: head}}
	in.Excluded = []string{"Marta"}
	if r = DecideGate(in); r.OK {
		t.Errorf("aprueba quien escribió un commit: %+v", r)
	}
	// Un dueño por correo no deja aprobar a cualquiera.
	in = base
	in.Owners = ParseCodeowners("*.yml dba@ejemplo.com\n")
	in.Reviews = []Review{{User: "mallory", State: "APPROVED", CommitID: head}}
	if r = DecideGate(in); r.OK || !strings.Contains(strings.Join(r.Notes, "\n"), "dba@ejemplo.com es dueño por correo") {
		t.Errorf("dueño por correo: %+v", r)
	}
	// Quien pide cambios y no se puede verificar como dueño también detiene.
	in.Owners = ParseCodeowners("* @org/secreto\n")
	in.Reviews = []Review{{User: "zoe", State: "CHANGES_REQUESTED"}}
	if r = DecideGate(in); r.OK || strings.Join(r.Blockers, ",") != "zoe" || !strings.Contains(strings.Join(r.Notes, "\n"), "@zoe pidió cambios y no pude verificar") {
		t.Errorf("pedido de cambios sin verificar: %+v", r)
	}
	// Sin CODEOWNERS vale cualquier persona que no abrió el PR.
	in = base
	in.Owners = nil
	in.Reviews = []Review{{User: "beto", State: "APPROVED", CommitID: head}}
	if r = DecideGate(in); r.OK || !strings.Contains(r.Notes[0], "no tiene CODEOWNERS") {
		t.Errorf("sin CODEOWNERS el autor no cuenta: %+v", r)
	}
	in.Reviews = append(in.Reviews, Review{User: "zoe", State: "APPROVED", CommitID: head})
	if r = DecideGate(in); !r.OK {
		t.Errorf("sin CODEOWNERS aprueba cualquiera: %+v", r)
	}
	// Riesgo solo por impacto: un grupo con los dueños de todo lo cambiado.
	in = GateInput{Author: "beto", HeadSHA: head, Owners: owners, Member: member, Changed: []string{"pagos/Cobro.java", "README.md"}, Impact: R2, ImpactWhy: "llega a 1 repo más"}
	r = DecideGate(in)
	if len(r.Groups) != 1 || strings.Join(r.Groups[0].Owners, " ") != "@ana @org/pagos" || len(r.Groups[0].Files) != 2 {
		t.Errorf("grupo por impacto: %+v", r)
	}
	// Con los archivos que mueven el impacto, revisan sus dueños, además de los de las rutas riesgosas.
	in.ImpactFiles = []string{"pagos/Cobro.java"}
	in.Files = []FileRisk{{Path: "CODEOWNERS", Risk: R3, Why: "quién revisa"}}
	in.Reviews = []Review{{User: "ana", State: "APPROVED", CommitID: head}}
	r = DecideGate(in)
	if len(r.Groups) != 2 || r.OK || !r.Groups[0].OK || r.Groups[1].OK || strings.Join(r.Groups[1].Owners, " ") != "@org/pagos" {
		t.Errorf("grupos por ruta y por impacto: %+v", r)
	}
	in.Reviews = append(in.Reviews, Review{User: "marta", State: "APPROVED", CommitID: head})
	if r = DecideGate(in); !r.OK {
		t.Errorf("cada grupo con su dueño: %+v", r)
	}
}

func TestRevisionesYEquipos(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/pulls/5/reviews":
			if r.URL.Query().Get("page") != "1" {
				_ = json.NewEncoder(w).Encode([]any{})
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"user": map[string]string{"login": "Ana"}, "state": "APPROVED", "commit_id": "c1"},
				{"user": map[string]string{"login": "ana"}, "state": "COMMENTED", "commit_id": "c2"},
				{"user": map[string]string{"login": "luis"}, "state": "APPROVED", "commit_id": "c1"},
				{"user": map[string]string{"login": "luis"}, "state": "DISMISSED", "commit_id": "c1"},
				{"user": map[string]string{"login": "eva"}, "state": "CHANGES_REQUESTED", "commit_id": "c2"},
			})
		case "/orgs/org/teams/pagos/memberships/ana":
			_ = json.NewEncoder(w).Encode(map[string]string{"state": "active"})
		case "/orgs/org/teams/pagos/memberships/eva":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
		case "/orgs/org/teams/pagos":
			_ = json.NewEncoder(w).Encode(map[string]string{"slug": "pagos"})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
		}
	}))
	defer srv.Close()
	c := &github.Client{Base: srv.URL, Token: "t"}
	rs, err := Reviews(context.Background(), c, "o/r", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].User != "ana" || rs[0].State != "APPROVED" || rs[0].CommitID != "c1" || rs[1].User != "eva" {
		t.Errorf("revisiones: %+v", rs)
	}
	if ok, err := TeamMember(context.Background(), c, "@org/pagos", "ana"); !ok || err != nil {
		t.Errorf("miembro: %v %v", ok, err)
	}
	if ok, err := TeamMember(context.Background(), c, "@org/pagos", "eva"); ok || err != nil {
		t.Errorf("no miembro de un equipo visible: %v %v", ok, err)
	}
	if _, err := TeamMember(context.Background(), c, "@org/oculto", "ana"); err != ErrUnverifiable {
		t.Errorf("equipo que el token no ve: %v", err)
	}
	if _, err := Reviews(context.Background(), c, "o/r?x", 5); err == nil {
		t.Error("repo inválido")
	}
	// 250 commits o más: no se sabe quién escribió todo; no hay lista.
	many := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		n := 100
		if page == "3" {
			n = 50
		}
		list := make([]map[string]any, n)
		for i := range list {
			list[i] = map[string]any{"author": map[string]string{"login": "dev" + page}}
		}
		_ = json.NewEncoder(w).Encode(list)
	}))
	defer many.Close()
	if _, err := CommitAuthors(context.Background(), &github.Client{Base: many.URL, Token: "t"}, "o/r", 5); err == nil || !strings.Contains(err.Error(), "250 commits") {
		t.Errorf("PR con 250 commits: %v", err)
	}
}

func TestPlanSinRutasDeRiesgo(t *testing.T) {
	head := strings.Repeat("h", 40)
	owners := ParseCodeowners("* @ana\n")
	in := GateInput{Author: "beto", HeadSHA: head, Owners: owners, Changed: []string{"docs/prod.md"}, PlanRisk: R3, PlanWhy: "reemplazar 1"}
	r := DecideGate(in)
	if !r.Required || r.OK || r.Risk != R3 || len(r.Groups) != 1 || strings.Join(r.Groups[0].Owners, " ") != "@ana" {
		t.Fatalf("un plan R3 sin rutas de riesgo espera a un dueño: %+v", r)
	}
	in.Reviews = []Review{{User: "ana", State: "APPROVED", CommitID: head}}
	if r = DecideGate(in); !r.OK {
		t.Errorf("con la aprobación del dueño en el último commit, pasa: %+v", r)
	}
	// Un riesgo que pide revisión nunca queda sin grupo.
	if r = DecideGate(GateInput{Author: "beto", HeadSHA: head, Impact: R2, ImpactWhy: "x"}); r.OK || len(r.Groups) == 0 {
		t.Errorf("sin archivos, igual pide una aprobación: %+v", r)
	}
	for _, p := range []string{"stacks/prod/main.tf.json", "environments/prod.tfvars", "live/prod/terragrunt.hcl", "stacks/demo/.terraform.lock.hcl", "coyote/infra.yaml", ".codex/hooks.json"} {
		if f := PathRisks([]string{p}, DefaultRules); len(f) != 1 || f[0].Risk != R3 {
			t.Errorf("%s es R3: %+v", p, f)
		}
	}
}
