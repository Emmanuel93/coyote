package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecide(t *testing.T) {
	c := Default()
	cases := []struct {
		name  string
		in    Input
		model string
		usd   float64
		deny  bool
		note  string
	}{
		{"modelo del agente", Input{Agent: "coyote-dev", AgentModel: "sonnet"}, "sonnet", 2, false, "el que declara"},
		{"sin modelo: el de defecto", Input{Agent: "x"}, "sonnet", 2, false, "por defecto"},
		{"piso por riesgo", Input{Agent: "coyote-scribe", AgentModel: "haiku", Risk: "R3"}, "opus", 2, false, "pide al menos opus"},
		{"al 80 % baja un nivel", Input{AgentModel: "opus", MonthlyUSD: 100, SpentUSD: 85}, "sonnet", 2, false, "baja de opus a sonnet"},
		{"R3 no baja", Input{AgentModel: "opus", Risk: "R3", MonthlyUSD: 100, SpentUSD: 85}, "opus", 2, false, "R3 no baja"},
		{"no baja del piso", Input{AgentModel: "sonnet", Risk: "R2", MonthlyUSD: 100, SpentUSD: 90}, "sonnet", 2, false, "mínimo permitido"},
		{"al 100 % no corre", Input{AgentModel: "haiku", MonthlyUSD: 50, SpentUSD: 50}, "haiku", 2, true, ""},
		{"80 % exacto en coma flotante", Input{AgentModel: "opus", MonthlyUSD: 0.05, SpentUSD: 0.04}, "sonnet", 0.01, false, "baja de opus a sonnet"},
		{"tope por lo que queda", Input{AgentModel: "haiku", MonthlyUSD: 50, SpentUSD: 49.5}, "haiku", 0.5, false, "lo que queda"},
		{"modelo fuera de niveles", Input{Model: "claude-sonnet-x", Risk: "R3"}, "claude-sonnet-x", 2, false, "no está en levels"},
	}
	for _, tc := range cases {
		d := c.Decide(tc.in)
		if d.Model != tc.model || (d.Refused != "") != tc.deny || (!tc.deny && d.MaxUSD != tc.usd) {
			t.Errorf("%s: %+v", tc.name, d)
		}
		if tc.note != "" && !strings.Contains(strings.Join(d.Notes, " | "), tc.note) {
			t.Errorf("%s: falta la nota %q en %v", tc.name, tc.note, d.Notes)
		}
	}
	if d := c.Decide(Input{AgentTurns: 12}); d.MaxTurns != 12 {
		t.Errorf("turnos del agente: %d", d.MaxTurns)
	}
	if d := c.Decide(Input{AgentTurns: 12, MaxTurns: 5}); d.MaxTurns != 5 {
		t.Errorf("turnos pedidos: %d", d.MaxTurns)
	}
}

func TestLoad(t *testing.T) {
	root := t.TempDir()
	c, err := Load(root)
	if err != nil || c.Default != "sonnet" {
		t.Fatalf("sin archivo valen los valores por defecto: %v %v", c, err)
	}
	write := func(s string) {
		p := filepath.Join(root, "coyote", "router.yaml")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(Template)
	if c, err = Load(root); err != nil || c.Limits.MaxUSD != 2 {
		t.Fatalf("la plantilla es válida: %v %v", c, err)
	}
	write("version: 1\nagents: {coyote-dev: gpt}\n")
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "no está en levels") {
		t.Errorf("un modelo fuera de levels se rechaza: %v", err)
	}
	write("version: 1\nbudget: {warn_at: 1.2, stop_at: 1.0}\n")
	if _, err := Load(root); err == nil {
		t.Error("warn_at debe ser menor que stop_at")
	}
	write("version: 1\nnada: 1\n")
	if _, err := Load(root); err == nil {
		t.Error("una clave desconocida se rechaza")
	}
}

func TestSplit(t *testing.T) {
	c := Default()
	if cost, ok := c.Split("claude-sonnet-x", 1.0, 1000, 0, 0, 1000); ok || cost.In != 1.0 || cost.Out != 0 {
		t.Errorf("sin precios todo es entrada: %+v %v", cost, ok)
	}
	c.Prices = map[string]Price{"sonnet": {Input: 3, Output: 15}}
	cost, ok := c.Split("claude-sonnet-x", 0.018, 1000, 0, 0, 1000)
	if !ok || cost.In+cost.Out < 0.0179 || cost.In+cost.Out > 0.0181 || cost.Out <= cost.In {
		t.Errorf("con precios se separa y el total se conserva: %+v", cost)
	}
}
