package pace

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) sleep(d time.Duration)   { c.t = c.t.Add(d) }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func limiter(t *testing.T, profile string) (*Limiter, *clock) {
	t.Helper()
	t.Setenv("COYOTE_STATE_DIR", t.TempDir())
	l, err := Open(profile, "github.com")
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	l.Now, l.Sleep = c.now, c.sleep
	return l, c
}

func TestMinGapAndPerMinute(t *testing.T) {
	l, c := limiter(t, "human")
	start := c.t
	for i := 0; i < 6; i++ {
		if _, err := l.Wait("write"); err != nil {
			t.Fatal(err)
		}
	}
	// Seis escrituras con 3 s de pausa mínima: la última a los 15 s.
	if got := c.t.Sub(start); got != 15*time.Second {
		t.Fatalf("pausas mínimas: %v", got)
	}
	// La séptima espera a que salga del minuto la primera.
	d, err := l.Wait("write")
	if err != nil {
		t.Fatal(err)
	}
	if c.t.Sub(start) != time.Minute || d != 45*time.Second {
		t.Fatalf("tope por minuto: esperó %v, reloj en %v", d, c.t.Sub(start))
	}
	if filepath.Base(l.Path) != "pace-github.com.json" {
		t.Fatalf("archivo de estado inesperado %s", l.Path)
	}
}

func TestReadsDoNotConsumeWrites(t *testing.T) {
	l, c := limiter(t, "human")
	start := c.t
	for i := 0; i < 20; i++ {
		if _, err := l.Wait("read"); err != nil {
			t.Fatal(err)
		}
	}
	if c.t != start {
		t.Fatal("las lecturas no deben esperar sin Retry-After")
	}
}

func TestRetryAfterAndReserve(t *testing.T) {
	l, c := limiter(t, "human")
	h := http.Header{}
	h.Set("Retry-After", "30")
	if err := l.Observe(h, http.StatusForbidden); err != nil {
		t.Fatal(err)
	}
	d, err := l.Wait("read")
	if err != nil || d != 30*time.Second {
		t.Fatalf("Retry-After: esperó %v, %v", d, err)
	}
	// Con la cuota bajo la reserva, se espera al reinicio; si es muy lejos, error claro.
	h = http.Header{}
	h.Set("X-RateLimit-Limit", "5000")
	h.Set("X-RateLimit-Remaining", "900")
	h.Set("X-RateLimit-Reset", strconv.FormatInt(c.t.Add(time.Hour).Unix(), 10))
	if err := l.Observe(h, http.StatusOK); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Wait("write"); !errors.Is(err, ErrBudget) {
		t.Fatalf("con la reserva agotada y reinicio en una hora debe fallar con ErrBudget: %v", err)
	}
	h.Set("X-RateLimit-Remaining", "3000")
	if err := l.Observe(h, http.StatusOK); err != nil {
		t.Fatal(err)
	}
	if d, err := l.Wait("write"); err != nil || d != 0 {
		t.Fatalf("con cuota suficiente no se espera: %v %v", d, err)
	}
}

func TestSecondaryLimitWithoutRetryAfter(t *testing.T) {
	l, _ := limiter(t, "batch")
	if err := l.Observe(http.Header{}, http.StatusTooManyRequests); err != nil {
		t.Fatal(err)
	}
	if d, err := l.Wait("write"); err != nil || d != time.Minute {
		t.Fatalf("límite secundario: se esperaba 1 minuto, %v %v", d, err)
	}
}
