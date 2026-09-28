package ccf

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	lines := []string{
		"2026-09-24T21:05Z|@ana/coyote-dev|W-023|shop-api|feat|payments|reintento de cobro con idempotencia|sha:2f09eb6 art:A-0142 apr:P-0091|12.4k/8.7k/1.1k|$0.009+$0.011|ok",
		"2026-09-24T21:12Z|@ana/coyote-reviewer|W-023|shop-api|rev|payments|2 hallazgos: idempotencia, timeout|art:A-0143 std:R7,R12|9.8k/7.9k/950|$0.009+$0.018|pend",
		"2026-09-24T21:20Z|@ana|W-023|hub|apr|A-0143|aprobado con comentario|apr:P-0092|-|-|ok",
		"2026-09-24T21:30Z|system|-|shop-api|idx|-|reindexado incremental|-|1.2M/0/0|$0.5+$0|ok",
	}
	for _, s := range lines {
		l, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if got := l.String(); got != s {
			t.Fatalf("ida y vuelta:\n obtenido %s\n esperado %s", got, s)
		}
	}
}

func TestValidate(t *testing.T) {
	base := "2026-09-24T21:05Z|@ana|W-1|repo|feat|core|algo nuevo|-|-|-|ok"
	if _, err := Parse(base); err != nil {
		t.Fatalf("línea base inválida: %v", err)
	}
	bad := map[string]string{
		"campos":   "2026-09-24T21:05Z|@ana|W-1|repo|feat|core|algo|-|-|ok",
		"actor":    strings.Replace(base, "@ana", "ana", 1),
		"tipo":     strings.Replace(base, "|feat|", "|feature|", 1),
		"estado":   strings.Replace(base, "|ok", "|done", 1),
		"what":     strings.Replace(base, "algo nuevo", "una dos tres cuatro cinco seis siete ocho nueve diez once doce trece", 1),
		"ref":      strings.Replace(base, "|-|-|-|ok", "|sin-clave|-|-|ok", 1),
		"tokens":   strings.Replace(base, "|-|-|ok", "|1k/2k/3|-|ok", 1),
		"ts":       strings.Replace(base, "2026-09-24T21:05Z", "ayer", 1),
		"costoneg": strings.Replace(base, "|-|ok", "|$-1+$0|ok", 1),
	}
	for name, s := range bad {
		if _, err := Parse(s); err == nil {
			t.Errorf("%s: se esperaba error en %q", name, s)
		}
	}
}

func TestCounts(t *testing.T) {
	for in, want := range map[string]int64{"950": 950, "12.4k": 12400, "1.2M": 1200000, "0": 0} {
		got, err := ParseCount(in)
		if err != nil || got != want {
			t.Errorf("ParseCount(%q) = %d, %v", in, got, err)
		}
	}
	if FormatCount(12000) != "12k" || FormatCount(999) != "999" || FormatCount(2_500_000) != "2.5M" {
		t.Error("FormatCount no compacta como se espera")
	}
	if FormatUSD(0.0090) != "0.009" || FormatUSD(1.5) != "1.5" || FormatUSD(0) != "0" {
		t.Error("FormatUSD no recorta ceros")
	}
	if got := ShortWhat("uno dos tres cuatro", 2); got != "uno dos…" {
		t.Errorf("ShortWhat = %q", got)
	}
}

func TestNumerosFueraDeRango(t *testing.T) {
	for _, s := range []string{"inf+0", "NaN+0", "0+-1", "2e9+0"} {
		if _, err := ParseCost(s); err == nil {
			t.Errorf("ParseCost(%q) debería fallar", s)
		}
	}
	for _, s := range []string{"inf/0/0", "nan/0/0", "9e18/0/0", "-1/0/0"} {
		if _, err := ParseTokens(s); err == nil {
			t.Errorf("ParseTokens(%q) debería fallar", s)
		}
	}
	l := Line{TS: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), Actor: "@ana", Project: "-", Repo: "demo", Type: "cost",
		Scope: "-", What: "costo", Status: "ok", Cost: &Cost{In: math.NaN(), Out: 0}}
	if l.Validate() == nil {
		t.Error("un costo NaN debe ser inválido")
	}
}
