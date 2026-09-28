// Package pace administra el ritmo de las operaciones remotas para que una
// cuenta se comporte como una persona y no como un bot: pocas escrituras por
// minuto y por hora, una pausa mínima entre escrituras, una reserva de cuota
// para el uso interactivo de la persona y respeto estricto a Retry-After y a
// los límites de GitHub. El estado es por cuenta, no por proyecto, y vive en
// el directorio de configuración del usuario, fuera de cualquier repo.
package pace

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/userdir"
)

// Profile define el ritmo permitido.
type Profile struct {
	Name            string
	WritesPerMinute int
	WritesPerHour   int
	MinGap          time.Duration
	Reserve         float64 // fracción de la cuota del proveedor que se deja para la persona
	MaxWait         time.Duration
}

// Profiles son los perfiles disponibles. GitHub permite hasta 80 escrituras por
// minuto y 500 por hora; el perfil human queda muy por debajo.
var Profiles = map[string]Profile{
	"human": {Name: "human", WritesPerMinute: 6, WritesPerHour: 120, MinGap: 3 * time.Second, Reserve: 0.2, MaxWait: 2 * time.Minute},
	"batch": {Name: "batch", WritesPerMinute: 20, WritesPerHour: 300, MinGap: time.Second, Reserve: 0.2, MaxWait: 5 * time.Minute},
}

// ErrBudget indica que esperar excedería el máximo razonable; se reintenta después.
var ErrBudget = errors.New("cuota a ritmo humano agotada")

// State es lo que se recuerda entre ejecuciones.
type State struct {
	Writes     []time.Time `json:"writes"`
	Remaining  int         `json:"remaining"`
	Limit      int         `json:"limit"`
	Reset      time.Time   `json:"reset"`
	RetryUntil time.Time   `json:"retry_until"`
}

// Limiter aplica un perfil con estado persistido.
type Limiter struct {
	Profile Profile
	Path    string
	Now     func() time.Time
	Sleep   func(time.Duration)
}

// Open devuelve el limitador de la cuenta host con el perfil indicado.
func Open(profile, host string) (*Limiter, error) {
	p, ok := Profiles[profile]
	if !ok {
		return nil, fmt.Errorf("perfil de ritmo desconocido %q (usa human o batch)", profile)
	}
	dir, err := userdir.StateDir()
	if err != nil {
		return nil, err
	}
	name := "pace-" + strings.NewReplacer("/", "_", ":", "_", "\\", "_").Replace(host) + ".json"
	return &Limiter{Profile: p, Path: filepath.Join(dir, name), Now: time.Now, Sleep: time.Sleep}, nil
}

func (l *Limiter) load() State {
	var s State
	if data, err := os.ReadFile(l.Path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func (l *Limiter) save(s State) error {
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return fsx.WriteAtomic(l.Path, data, 0o600)
}

// lock serializa a varios procesos de coyote sobre el mismo estado.
func (l *Limiter) lock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return nil, err
	}
	lk := l.Path + ".lock"
	for i := 0; i < 50; i++ {
		f, err := os.OpenFile(lk, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { _ = os.Remove(lk) }, nil
		}
		if info, err := os.Stat(lk); err == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(lk) // un proceso murió con el lock tomado
			continue
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("el estado de ritmo está ocupado por otro proceso (%s)", lk)
}

// Delay calcula cuánto hay que esperar antes de una operación, sin esperar.
// kind es "write" o "read"; las lecturas solo respetan Retry-After y la reserva.
func (l *Limiter) Delay(kind string, s State) (time.Duration, string) {
	now := l.Now()
	at := now
	why := ""
	later := func(t time.Time, reason string) {
		if t.After(at) {
			at, why = t, reason
		}
	}
	later(s.RetryUntil, "el proveedor pidió esperar (Retry-After)")
	if s.Limit > 0 && s.Remaining >= 0 && float64(s.Remaining) <= l.Profile.Reserve*float64(s.Limit) && s.Reset.After(now) {
		later(s.Reset, fmt.Sprintf("queda %d de %d en la cuota; se reserva el %.0f%% para ti", s.Remaining, s.Limit, l.Profile.Reserve*100))
	}
	if kind == "write" {
		w := recent(s.Writes, now, time.Hour)
		if n := len(w); n > 0 {
			later(w[n-1].Add(l.Profile.MinGap), "pausa mínima entre escrituras")
		}
		if lastMin := recent(w, now, time.Minute); len(lastMin) >= l.Profile.WritesPerMinute {
			later(lastMin[len(lastMin)-l.Profile.WritesPerMinute].Add(time.Minute), fmt.Sprintf("tope de %d escrituras por minuto", l.Profile.WritesPerMinute))
		}
		if len(w) >= l.Profile.WritesPerHour {
			later(w[len(w)-l.Profile.WritesPerHour].Add(time.Hour), fmt.Sprintf("tope de %d escrituras por hora", l.Profile.WritesPerHour))
		}
	}
	return at.Sub(now), why
}

// Wait espera lo necesario y registra la operación. Si la espera supera
// MaxWait devuelve ErrBudget con el momento en que conviene reintentar.
//
// El turno se reserva y se guarda antes de dormir, y el lock se suelta durante
// la espera: otro proceso ve la escritura reservada y se forma detrás de ella.
func (l *Limiter) Wait(kind string) (time.Duration, error) {
	unlock, err := l.lock()
	if err != nil {
		return 0, err
	}
	s := l.load()
	d, why := l.Delay(kind, s)
	if d > l.Profile.MaxWait {
		unlock()
		return 0, fmt.Errorf("%w: %s; reintenta después de %s", ErrBudget, why, when(l.Now(), l.Now().Add(d)))
	}
	if kind == "write" {
		now := l.Now()
		s.Writes = append(recent(s.Writes, now, time.Hour), now.Add(d))
		if err := l.save(s); err != nil {
			unlock()
			return 0, err
		}
	}
	unlock()
	if d > 0 {
		l.Sleep(d)
	}
	return d, nil
}

// when muestra la hora, con la fecha si no es hoy.
func when(now, t time.Time) string {
	if t.Format("2006-01-02") == now.Format("2006-01-02") {
		return t.Format("15:04:05")
	}
	return t.Format("2006-01-02 15:04:05")
}

// maxRetry acota lo que un proveedor puede pedir esperar: un Retry-After absurdo
// no bloquea la cuenta por años.
const maxRetry = 24 * time.Hour

// Observe guarda lo que el proveedor informó: cuota restante, reinicio y Retry-After.
func (l *Limiter) Observe(h http.Header, status int) error {
	unlock, err := l.lock()
	if err != nil {
		return err
	}
	defer unlock()
	s := l.load()
	now := l.Now()
	if v, err := strconv.Atoi(h.Get("X-RateLimit-Remaining")); err == nil {
		s.Remaining = v
	}
	if v, err := strconv.Atoi(h.Get("X-RateLimit-Limit")); err == nil {
		s.Limit = v
	}
	if v, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil && v > 0 {
		if r := time.Unix(v, 0); r.Before(now.Add(maxRetry)) {
			s.Reset = r
		} else {
			s.Reset = now.Add(maxRetry)
		}
	}
	if ra := h.Get("Retry-After"); ra != "" {
		if secs, err := strconv.ParseInt(ra, 10, 64); err == nil && secs >= 0 {
			if time.Duration(secs) > maxRetry/time.Second {
				secs = int64(maxRetry / time.Second)
			}
			s.RetryUntil = now.Add(time.Duration(secs) * time.Second)
		} else if t, err := http.ParseTime(ra); err == nil && t.After(now) {
			if t.After(now.Add(maxRetry)) {
				t = now.Add(maxRetry)
			}
			s.RetryUntil = t
		}
	} else if (status == http.StatusForbidden || status == http.StatusTooManyRequests) && s.Remaining == 0 && !s.Reset.IsZero() {
		s.RetryUntil = s.Reset
	} else if status == http.StatusForbidden || status == http.StatusTooManyRequests {
		// Límite secundario sin Retry-After: GitHub pide esperar al menos un minuto.
		s.RetryUntil = now.Add(time.Minute)
	}
	return l.save(s)
}

// RetryAt devuelve desde cuándo se puede volver a llamar al proveedor.
func (l *Limiter) RetryAt() time.Time {
	d, _ := l.Delay("read", l.load())
	return l.Now().Add(d)
}

// Status resume el estado para mostrarlo.
func (l *Limiter) Status() string {
	s := l.load()
	now := l.Now()
	w := recent(s.Writes, now, time.Hour)
	out := fmt.Sprintf("perfil %s · %d escrituras en la última hora (tope %d) · %d en el último minuto (tope %d)",
		l.Profile.Name, len(w), l.Profile.WritesPerHour, len(recent(w, now, time.Minute)), l.Profile.WritesPerMinute)
	if s.Limit > 0 {
		out += fmt.Sprintf(" · cuota del proveedor %d/%d", s.Remaining, s.Limit)
	}
	if s.RetryUntil.After(now) {
		out += " · esperar hasta " + s.RetryUntil.Format("15:04:05")
	}
	return out
}

// recent devuelve las escrituras de la ventana, incluidas las reservadas hacia
// adelante (hasta 10 minutos); fechas más lejanas son estado corrupto o reloj movido.
func recent(ts []time.Time, now time.Time, window time.Duration) []time.Time {
	var out []time.Time
	for _, t := range ts {
		if now.Sub(t) < window && !t.After(now.Add(10*time.Minute)) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}
