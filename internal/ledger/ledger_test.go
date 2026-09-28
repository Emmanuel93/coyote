package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccf"
)

func TestAppendAndRead(t *testing.T) {
	root := t.TempDir()
	l := Open(root)
	ts := time.Date(2026, 9, 27, 18, 5, 0, 0, time.UTC)
	line := ccf.Line{TS: ts, Actor: "@ana/coyote-dev", Project: "W-0001", Repo: "demo", Type: "feat",
		Scope: "core", What: "primer evento", Status: "ok", Tokens: &ccf.Tokens{In: 12400, Cache: 8700, Out: 1100},
		Cost: &ccf.Cost{In: 0.009, Out: 0.011}}
	p, err := l.Append(line, "ana")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, "coyote/ledger/2026/09/27-ana.ccf") {
		t.Fatalf("ruta inesperada %s", p)
	}
	line.TS = ts.Add(time.Minute)
	line.Type = "fix"
	if _, err := l.Append(line, "ana"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if strings.Count(string(data), ccf.Header) != 1 {
		t.Fatalf("el encabezado debe escribirse una sola vez:\n%s", data)
	}
	entries, probs, err := l.ReadAll()
	if err != nil || len(probs) != 0 || len(entries) != 2 {
		t.Fatalf("lectura: %d eventos, %v, %v", len(entries), probs, err)
	}
	tk, c := Totals(entries)
	if tk.In != 24800 || c.Total() < 0.0399 || c.Total() > 0.0401 {
		t.Fatalf("totales inesperados: %+v %+v", tk, c)
	}
	summary := entries[1]
	summary.Line.Type = "close"
	if tk2, c2 := Totals(append(entries, summary)); tk2 != tk || c2 != c {
		t.Fatalf("un cierre no debe sumarse dos veces: %+v %+v", tk2, c2)
	}
	bad := line
	bad.Type = "desconocido"
	if _, err := l.Append(bad, "ana"); err == nil {
		t.Fatal("se aceptó un evento inválido")
	}
}

func TestAppendSinSaltoFinal(t *testing.T) {
	root := t.TempDir()
	l := Open(root)
	ts := time.Date(2026, 9, 27, 18, 5, 0, 0, time.UTC)
	p := l.PathFor(ts, "ana")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	// Archivo editado a mano: encabezado y un evento, sin salto de línea final.
	prev := ccf.Header + "\n2026-09-27T18:00Z|@ana|-|demo|note|-|nota a mano|-|-|-|ok"
	if err := os.WriteFile(p, []byte(prev), 0o644); err != nil {
		t.Fatal(err)
	}
	line := ccf.Line{TS: ts, Actor: "@ana", Project: "-", Repo: "demo", Type: "fix", Scope: "-", What: "segundo", Status: "ok"}
	if _, err := l.Append(line, "ana"); err != nil {
		t.Fatal(err)
	}
	entries, probs, err := l.ReadAll()
	if err != nil || len(probs) != 0 || len(entries) != 2 {
		t.Fatalf("se esperaban 2 eventos válidos: %d, %v, %v", len(entries), probs, err)
	}
}
