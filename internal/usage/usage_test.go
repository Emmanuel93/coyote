package usage

import (
	"math"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccf"
)

func ev(repo, actor, typ, model string, in, cache, out int64, cin, cout float64, day int) Event {
	l := ccf.Line{TS: time.Date(2026, 9, day, 12, 0, 0, 0, time.UTC), Actor: actor, Repo: repo, Type: typ, Scope: "-", What: "x", Status: "ok",
		Tokens: &ccf.Tokens{In: in, Cache: cache, Out: out}, Cost: &ccf.Cost{In: cin, Out: cout}}
	if model != "" {
		l.Refs = []string{"model:" + model}
	}
	return Event{Repo: repo, Line: l}
}

func TestGroupBothWays(t *testing.T) {
	events := []Event{
		ev("tienda", "@ana/coyote-dev", "run", "sonnet-5", 1000, 800, 100, 0.001, 0.002, 20),
		ev("tienda", "@luis", "run", "opus-5.5", 2000, 0, 200, 0.008, 0.004, 21),
		ev("pagos", "@ana/coyote-reviewer", "rev", "haiku-4.5", 500, 100, 50, 0.0005, 0.0005, 22),
		ev("pagos", "@ana", "close", "", 3500, 900, 350, 0.0095, 0.0065, 22), // resumen: no suma
	}
	events = Filter(events, time.Time{})
	if len(events) != 3 {
		t.Fatalf("el cierre no debe contarse: %d eventos", len(events))
	}
	byProject, all := Group(events, ByRepo, ByPerson)
	if all.Events != 3 || all.Tokens.In != 3500 || math.Abs(all.CostTotal()-0.016) > 1e-9 {
		t.Fatalf("totales: %+v", all)
	}
	if byProject[0].Key != "tienda" || len(byProject[0].Children) != 2 || byProject[0].Children[0].Key != "@luis" {
		t.Fatalf("proyecto > persona: %+v", byProject)
	}
	byPerson, _ := Group(events, ByPerson, ByRepo)
	if byPerson[0].Key != "@luis" || byPerson[1].Key != "@ana" || len(byPerson[1].Children) != 2 {
		t.Fatalf("persona > proyecto: %+v", byPerson)
	}
	if byPerson[1].Models["sonnet-5"] != 1 || byPerson[1].Models["haiku-4.5"] != 1 {
		t.Fatalf("modelos por persona: %+v", byPerson[1].Models)
	}
	if got := Filter(events, time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)); len(got) != 2 {
		t.Fatalf("filtro por fecha: %d", len(got))
	}
	if Person("@ana/coyote-dev") != "@ana" || Agent("@ana/coyote-dev") != "coyote-dev" || Agent("@ana") != "" {
		t.Fatal("Person/Agent")
	}
	if s := all.CacheShare(); math.Abs(s-900.0/3500.0) > 1e-9 {
		t.Fatalf("CacheShare = %v", s)
	}
}
