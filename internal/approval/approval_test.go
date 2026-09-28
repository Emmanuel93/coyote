package approval

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/ledger"
)

func newStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	s := &Store{Root: t.TempDir(), Project: "tienda", Key: bytes.Repeat([]byte{7}, 32), Now: func() time.Time { return now }}
	return s, &now
}

const h1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func TestApproveVerifyAndTamper(t *testing.T) {
	s, now := newStore(t)
	p, isNew, err := s.Enqueue(Proposal{Hash: h1, Object: "Bash: make", Tool: "Bash", Kind: "shell", RequestedBy: "@ana/coyote-dev"})
	if err != nil || !isNew || !strings.HasPrefix(p.ID, "P-") {
		t.Fatalf("enqueue: %+v %v %v", p, isNew, err)
	}
	if again, isNew, _ := s.Enqueue(Proposal{Hash: h1}); isNew || again.ID != p.ID || again.Attempts != 2 {
		t.Fatalf("el mismo hash debe reutilizar la propuesta: %+v", again)
	}
	r, err := s.Approve(p, "@ana", 2, time.Hour, "W-0003")
	if err != nil {
		t.Fatal(err)
	}
	if q, _ := s.Queue(); len(q) != 0 {
		t.Fatal("aprobar saca la propuesta de la cola")
	}
	recs, probs, err := s.Records()
	if err != nil || len(recs) != 1 || len(probs) != 0 {
		t.Fatalf("records: %v %v %v", recs, probs, err)
	}
	st := s.Statuses(recs, map[string]int{r.ID: 1}, nil)
	if got, ok := s.Find(h1, st); !ok || got.Left != 1 {
		t.Fatalf("debe quedar un uso: %+v %v", got, ok)
	}
	if _, ok := s.Find(h1, s.Statuses(recs, map[string]int{r.ID: 2}, nil)); ok {
		t.Fatal("agotada no se encuentra")
	}
	if _, ok := s.Find(h1, s.Statuses(recs, nil, map[string]bool{r.ID: true})); ok {
		t.Fatal("revocada no se encuentra")
	}
	*now = now.Add(61 * time.Minute)
	if _, ok := s.Find(h1, s.Statuses(recs, nil, nil)); ok {
		t.Fatal("vencida no se encuentra")
	}
	// Alterar cualquier campo rompe la firma; otra clave o proyecto no validan.
	for _, mut := range []func(*Record){
		func(r *Record) { r.Uses = 99 }, func(r *Record) { r.Expires = "2026-09-30T10:00:00Z" },
		func(r *Record) { r.Hash = strings.Replace(r.Hash, "1", "2", 1) }, func(r *Record) { r.Approver = "@otro" },
	} {
		x := r
		mut(&x)
		if s.Verify(x) == nil {
			t.Errorf("un registro alterado validó: %+v", x)
		}
	}
	other := *s
	other.Key = bytes.Repeat([]byte{8}, 32)
	if other.Verify(r) == nil {
		t.Error("la firma de otra máquina no debe validar")
	}
	other = *s
	other.Project = "otro"
	if other.Verify(r) == nil {
		t.Error("un registro de otro proyecto no debe validar")
	}
	// Un registro con vigencia de más de 24 h, aunque esté bien firmado, no vale.
	long := r
	long.Expires = "2026-09-30T10:00:00Z"
	long.MAC = sign(s.Key, long)
	if s.Verify(long) == nil {
		t.Error("una vigencia mayor a 24 h no debe validar")
	}
	if _, err := s.Approve(Proposal{ID: r.ID, Hash: h1}, "@ana", 1, time.Hour, ""); err == nil {
		t.Error("un registro existente no se reescribe")
	}
	for _, bad := range []struct {
		uses int
		dur  time.Duration
	}{{0, time.Hour}, {101, time.Hour}, {1, 25 * time.Hour}, {1, 0}} {
		if _, err := s.Approve(Proposal{ID: "P-zzzzzz", Hash: h1}, "@ana", bad.uses, bad.dur, ""); err == nil {
			t.Errorf("límites no aplicados: %+v", bad)
		}
	}
}

func TestQueueTTLAndPrefix(t *testing.T) {
	s, now := newStore(t)
	a, _, _ := s.Enqueue(Proposal{ID: "P-aaaaa1", Hash: h1})
	b, _, _ := s.Enqueue(Proposal{ID: "P-aaaab2", Hash: strings.Replace(h1, "1", "3", 1)})
	if _, err := s.Get("aaaa"); err == nil {
		t.Error("un prefijo ambiguo debe fallar")
	}
	if p, err := s.Get("aaaab"); err != nil || p.ID != b.ID {
		t.Errorf("prefijo único: %+v %v", p, err)
	}
	if _, err := s.Reject(a, "@ana", "no"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(25 * time.Hour)
	q, _ := s.Queue()
	if len(q) != 1 || q[0].ID != b.ID {
		t.Errorf("un rechazo se olvida a las 24 h y una pendiente sigue: %+v", q)
	}
	*now = now.Add(7 * 24 * time.Hour)
	if q, _ := s.Queue(); len(q) != 0 {
		t.Errorf("una pendiente vence a los 7 días: %+v", q)
	}
	if _, err := s.Get("../../etc"); err == nil {
		t.Error("un id con ruta no debe aceptarse")
	}
}

func TestUsageFromLedger(t *testing.T) {
	line := func(typ, status string, refs ...string) ledger.Entry {
		return ledger.Entry{Line: ccf.Line{Type: typ, Status: status, Refs: refs}}
	}
	used, revoked := Usage([]ledger.Entry{
		line("gate", "ok", "apr:P-a", "hash:x"), line("gate", "ok", "apr:P-a"), line("gate", "fail", "apr:P-a"),
		line("apr", "ok", "apr:P-a"), line("rej", "ok", "apr:P-b"), line("rej", "ok", "prop:P-c"), line("gate", "pend", "prop:P-c"),
	})
	if used["P-a"] != 2 || !revoked["P-b"] || revoked["P-c"] || len(revoked) != 1 {
		t.Errorf("uso o revocación mal contados: %v %v", used, revoked)
	}
}

func TestLockSerializes(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	inside, max := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := Lock(root)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			inside++
			if inside > max {
				max = inside
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			inside--
			mu.Unlock()
			unlock()
		}()
	}
	wg.Wait()
	if max != 1 {
		t.Errorf("el lock dejó entrar a %d a la vez", max)
	}
	// Un lock abandonado hace más de 30 s no bloquea para siempre.
	lk := filepath.Join(root, ".coyote", "gate.lock")
	if err := os.WriteFile(lk, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	_ = os.Chtimes(lk, old, old)
	unlock, err := Lock(root)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
