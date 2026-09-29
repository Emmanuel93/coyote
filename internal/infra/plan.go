package infra

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// El plan de Terraform en JSON (terraform show -json plan.tfplan) lleva los
// valores de los recursos, secretos incluidos. coyote lo lee sin copiarlo y
// solo reporta direcciones, tipos y acciones.

// Change es un recurso que el plan cambia.
type Change struct {
	Address string `json:"address"`
	Type    string `json:"type"`
	Action  string `json:"action"` // crear, cambiar, reemplazar, destruir
	Risk    string `json:"risk"`
	Why     string `json:"why,omitempty"`
}

// PlanSummary es la clasificación de un plan.
type PlanSummary struct {
	Risk    string         `json:"risk"`
	Counts  map[string]int `json:"counts"`
	Changes []Change       `json:"changes"`
	Costly  []string       `json:"costly,omitempty"` // tipos que suelen mover el costo
}

// Acciones de un recurso.
const (
	ActCreate  = "crear"
	ActUpdate  = "cambiar"
	ActReplace = "reemplazar"
	ActDelete  = "destruir"
)

type planJSON struct {
	FormatVersion   string `json:"format_version"`
	ResourceChanges []struct {
		Address string `json:"address"`
		Mode    string `json:"mode"`
		Type    string `json:"type"`
		Change  struct {
			Actions []string        `json:"actions"`
			After   json.RawMessage `json:"after"`
		} `json:"change"`
	} `json:"resource_changes"`
}

var (
	iamRe     = regexp.MustCompile(`(^|_)(iam|role|roles|policy|policies|service_account_key|access_key|role_assignment|role_definition|user_assigned_identity)(_|$)`)
	keysRe    = regexp.MustCompile(`(^|_)(kms|key_ring|crypto_key|secret|secrets|secret_version|key_vault|keyvault|certificate)(_|$)`)
	netRe     = regexp.MustCompile(`(^|_)(firewall|security_group|security_group_rule|network_security_rule|network_security_group|ingress|access_level|security_policy)(_|$)`)
	dataRe    = regexp.MustCompile(`(^|_)(sql|database|db_instance|rds|postgres|postgresql|mysql|spanner|bigtable|dynamodb|storage_bucket|s3_bucket)(_|$)`)
	costlyRe  = regexp.MustCompile(`(^|_)(container_cluster|container_node_pool|eks_cluster|eks_node_group|kubernetes_cluster|node_pool|sql_database_instance|db_instance|rds_cluster|compute_instance|instance|nat|router_nat|nat_gateway|forwarding_rule|lb|load_balancer|redis|memorystore|elasticache|msk|kafka)(_|$)`)
	openCIDRs = regexp.MustCompile(`"(0\.0\.0\.0/0|::/0)"`)
	publicRe  = regexp.MustCompile(`"(allUsers|allAuthenticatedUsers)"`)
)

// ReadPlan lee y clasifica un plan en JSON.
func ReadPlan(r io.Reader) (*PlanSummary, error) {
	var p planJSON
	dec := json.NewDecoder(io.LimitReader(r, 256<<20))
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("no es un plan de Terraform en JSON (terraform show -json): %w", err)
	}
	if p.FormatVersion == "" {
		return nil, fmt.Errorf("no es un plan de Terraform en JSON: falta format_version")
	}
	s := &PlanSummary{Risk: "R1", Counts: map[string]int{}}
	costly := map[string]bool{}
	for _, rc := range p.ResourceChanges {
		if rc.Mode == "data" {
			continue
		}
		act := action(rc.Change.Actions)
		if act == "" {
			continue
		}
		c := Change{Address: rc.Address, Type: rc.Type, Action: act, Risk: "R2"}
		after := string(rc.Change.After)
		switch {
		case act == ActDelete || act == ActReplace:
			c.Risk, c.Why = "R3", "se "+map[string]string{ActDelete: "destruye", ActReplace: "reemplaza"}[act]
		case iamRe.MatchString(rc.Type):
			c.Risk, c.Why = "R3", "permisos (IAM)"
		case keysRe.MatchString(rc.Type):
			c.Risk, c.Why = "R3", "llaves o secretos"
		case netRe.MatchString(rc.Type) && openCIDRs.MatchString(after):
			c.Risk, c.Why = "R3", "abre la red a internet (0.0.0.0/0)"
		case publicRe.MatchString(after):
			c.Risk, c.Why = "R3", "acceso público (allUsers)"
		case dataRe.MatchString(rc.Type) && strings.Contains(after, `"deletion_protection":false`):
			c.Risk, c.Why = "R3", "datos sin protección contra borrado"
		case netRe.MatchString(rc.Type):
			c.Why = "reglas de red"
		}
		if costlyRe.MatchString(rc.Type) && act != ActDelete {
			costly[rc.Type] = true
		}
		s.Counts[act]++
		s.Changes = append(s.Changes, c)
		if c.Risk == "R3" {
			s.Risk = "R3"
		} else if s.Risk == "R1" {
			s.Risk = "R2"
		}
	}
	sort.SliceStable(s.Changes, func(i, j int) bool {
		if s.Changes[i].Risk != s.Changes[j].Risk {
			return s.Changes[i].Risk > s.Changes[j].Risk
		}
		return s.Changes[i].Address < s.Changes[j].Address
	})
	for t := range costly {
		s.Costly = append(s.Costly, t)
	}
	sort.Strings(s.Costly)
	return s, nil
}

// action traduce las acciones de un recurso; "" si no cambia.
func action(acts []string) string {
	switch strings.Join(acts, ",") {
	case "create":
		return ActCreate
	case "update":
		return ActUpdate
	case "delete":
		return ActDelete
	case "delete,create", "create,delete":
		return ActReplace
	}
	return ""
}

// Headline resume el plan en una frase: qué hace y su riesgo.
func (s *PlanSummary) Headline() string {
	if len(s.Changes) == 0 {
		return "el plan no cambia recursos"
	}
	var parts []string
	for _, a := range []string{ActCreate, ActUpdate, ActReplace, ActDelete} {
		if n := s.Counts[a]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", a, n))
		}
	}
	return strings.Join(parts, ", ")
}

// Reasons son los motivos de R3, con cuántos recursos cada uno.
func (s *PlanSummary) Reasons() []string {
	counts := map[string]int{}
	var order []string
	for _, c := range s.Changes {
		if c.Risk != "R3" {
			continue
		}
		if counts[c.Why] == 0 {
			order = append(order, c.Why)
		}
		counts[c.Why]++
	}
	out := make([]string, len(order))
	for i, w := range order {
		out[i] = fmt.Sprintf("%s (%d)", w, counts[w])
	}
	return out
}
