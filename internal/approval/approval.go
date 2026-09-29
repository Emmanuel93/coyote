// Package approval guarda las aprobaciones humanas de acciones exactas
// (ADR-0009): la cola de propuestas vive en .coyote/proposals (local, fuera
// de git), cada aprobación es un registro firmado en coyote/approvals y sus
// usos y revocaciones quedan en el ledger, que solo agrega líneas.
package approval

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/ledger"
)

// Límites de una aprobación.
const (
	MaxUses     = 100
	MaxDuration = 24 * time.Hour
	// PendingTTL es cuánto espera una propuesta en la cola; RejectedTTL, cuánto
	// se recuerda un rechazo para devolverle el motivo al agente.
	PendingTTL  = 7 * 24 * time.Hour
	RejectedTTL = 24 * time.Hour
	// maxStoredInput acota lo que se guarda de la entrada para revisarla.
	maxStoredInput = 2 << 20
)

// Record es una aprobación de una acción exacta.
type Record struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Hash        string `json:"hash"`
	Object      string `json:"object"`
	Tool        string `json:"tool"`
	Project     string `json:"project"`
	Root        string `json:"root"` // huella de la carpeta del proyecto en esta máquina
	WS          string `json:"ws,omitempty"`
	RequestedBy string `json:"requested_by,omitempty"`
	Approver    string `json:"approver"`
	Via         string `json:"via"`
	TS          string `json:"ts"`
	Expires     string `json:"expires"`
	Uses        int    `json:"uses"`
	MAC         string `json:"mac,omitempty"`
}

// Proposal es una acción que un agente pidió y espera decisión.
type Proposal struct {
	ID           string         `json:"id"`
	Hash         string         `json:"hash"`
	Object       string         `json:"object"`
	Tool         string         `json:"tool"`
	Kind         string         `json:"kind"`
	Path         string         `json:"path,omitempty"`
	Cwd          string         `json:"cwd,omitempty"`
	Command      string         `json:"command,omitempty"`
	Input        map[string]any `json:"input,omitempty"`
	InputOmitted bool           `json:"input_omitted,omitempty"`
	RequestedBy  string         `json:"requested_by"`
	IDE          string         `json:"ide,omitempty"`
	Session      string         `json:"session,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	First        time.Time      `json:"first_seen"`
	Last         time.Time      `json:"last_seen"`
	Attempts     int            `json:"attempts"`
	Logged       time.Time      `json:"logged,omitempty"`
	Status       string         `json:"status"` // pending o rejected
	RejectReason string         `json:"reject_reason,omitempty"`
	RejectedBy   string         `json:"rejected_by,omitempty"`
	RejectedAt   time.Time      `json:"rejected_at,omitempty"`
}

// Store da acceso a la cola y a los registros de un proyecto.
type Store struct {
	Root    string
	Project string
	Key     []byte
	Now     func() time.Time
	Rand    io.Reader
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Store) queueDir() string  { return filepath.Join(s.Root, ".coyote", "proposals") }
func (s *Store) recordDir() string { return filepath.Join(s.Root, "coyote", "approvals") }

// NewID genera un identificador corto: P- y seis caracteres en base 32.
func (s *Store) NewID() (string, error) {
	r := s.Rand
	if r == nil {
		r = rand.Reader
	}
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 6)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return "P-" + string(b), nil
}

// rootPrint identifica la carpeta del proyecto sin escribir su ruta: dos
// proyectos con el mismo nombre en la misma máquina no comparten aprobaciones.
func (s *Store) rootPrint() string {
	root := s.Root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	h := sha256.Sum256([]byte(root))
	return hex.EncodeToString(h[:8])
}

// sign calcula la firma HMAC del registro sin su campo mac.
func sign(key []byte, r Record) string {
	r.MAC = ""
	b, _ := json.Marshal(r)
	m := hmac.New(sha256.New, key)
	m.Write(b)
	return "hmac-sha256:" + hex.EncodeToString(m.Sum(nil))
}

// Verify revisa firma, forma y vigencia de un registro.
func (s *Store) Verify(r Record) error {
	if r.Type != "action" {
		return errors.New("no es una aprobación de acción")
	}
	if !hmac.Equal([]byte(sign(s.Key, r)), []byte(r.MAC)) {
		return errors.New("firma inválida: el registro no se aprobó en esta máquina o fue modificado")
	}
	if r.Project != s.Project || r.Root != s.rootPrint() {
		return fmt.Errorf("es de otro proyecto o de otra copia (%s)", r.Project)
	}
	ts, err1 := time.Parse(time.RFC3339, r.TS)
	exp, err2 := time.Parse(time.RFC3339, r.Expires)
	if err1 != nil || err2 != nil || !exp.After(ts) || exp.Sub(ts) > MaxDuration+time.Minute {
		return errors.New("fechas inválidas")
	}
	if r.Uses < 1 || r.Uses > MaxUses {
		return fmt.Errorf("usos fuera de rango (%d)", r.Uses)
	}
	if !strings.HasPrefix(r.Hash, "sha256:") || len(r.Hash) != len("sha256:")+64 {
		return errors.New("hash inválido")
	}
	return nil
}

// Records lee los registros de acción de coyote/approvals. Los de gate de
// release (P-0001...) no son de acción y se ignoran; los que no validan se
// devuelven como problemas.
func (s *Store) Records() ([]Record, map[string]error, error) {
	problems := map[string]error{}
	entries, err := os.ReadDir(s.recordDir())
	if os.IsNotExist(err) {
		return nil, problems, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var out []Record
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := fsx.ReadFile(s.Root, "coyote/approvals/"+name, 1<<20)
		if err != nil {
			problems[name] = err
			continue
		}
		var r Record
		if err := json.Unmarshal(data, &r); err != nil {
			problems[name] = err
			continue
		}
		if r.Type != "action" {
			continue
		}
		if err := s.Verify(r); err != nil {
			problems[name] = err
			continue
		}
		if r.ID+".json" != name {
			problems[name] = errors.New("el nombre del archivo no coincide con su id")
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out, problems, nil
}

// Usage resume del ledger cuántas veces se usó cada aprobación y cuáles se
// revocaron. Un uso es un evento gate ok con apr:<id>; una revocación, un
// evento rej con apr:<id>.
func Usage(entries []ledger.Entry) (used map[string]int, revoked map[string]bool) {
	used, revoked = map[string]int{}, map[string]bool{}
	for _, e := range entries {
		for _, r := range e.Line.Refs {
			id, ok := strings.CutPrefix(r, "apr:")
			if !ok {
				continue
			}
			switch {
			case e.Line.Type == "gate" && e.Line.Status == "ok":
				used[id]++
			case e.Line.Type == "rej":
				revoked[id] = true
			}
		}
	}
	return used, revoked
}

// Status describe una aprobación en un momento dado.
type Status struct {
	Record
	Left    int
	Revoked bool
	Expired bool
}

// Active dice si la aprobación todavía se puede usar.
func (st Status) Active() bool { return !st.Revoked && !st.Expired && st.Left > 0 }

// Statuses combina registros y ledger.
func (s *Store) Statuses(records []Record, used map[string]int, revoked map[string]bool) []Status {
	now := s.now()
	out := make([]Status, 0, len(records))
	for _, r := range records {
		exp, _ := time.Parse(time.RFC3339, r.Expires)
		out = append(out, Status{Record: r, Left: r.Uses - used[r.ID], Revoked: revoked[r.ID], Expired: !now.Before(exp)})
	}
	return out
}

// Find busca una aprobación vigente para el hash.
func (s *Store) Find(hash string, statuses []Status) (Status, bool) {
	for _, st := range statuses {
		if st.Hash == hash && st.Active() {
			return st, true
		}
	}
	return Status{}, false
}

// Approve convierte una propuesta en un registro firmado y la saca de la cola.
func (s *Store) Approve(p Proposal, approver string, uses int, dur time.Duration, ws string) (Record, error) {
	if uses < 1 || uses > MaxUses {
		return Record{}, fmt.Errorf("--uses va de 1 a %d", MaxUses)
	}
	if dur <= 0 || dur > MaxDuration {
		return Record{}, fmt.Errorf("--for va de 1 minuto a %s", MaxDuration)
	}
	now := s.now().Truncate(time.Second)
	r := Record{ID: p.ID, Type: "action", Hash: p.Hash, Object: p.Object, Tool: p.Tool, Project: s.Project, Root: s.rootPrint(),
		WS: ws, RequestedBy: p.RequestedBy, Approver: approver, Via: "cli", TS: now.Format(time.RFC3339),
		Expires: now.Add(dur).Format(time.RFC3339), Uses: uses}
	r.MAC = sign(s.Key, r)
	rel := "coyote/approvals/" + r.ID + ".json"
	if err := fsx.NoSymlinks(s.Root, rel); err != nil {
		return Record{}, err
	}
	dst := filepath.Join(s.Root, filepath.FromSlash(rel))
	if _, err := os.Lstat(dst); err == nil {
		return Record{}, fmt.Errorf("%s ya existe; los registros no se reescriben", rel)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Record{}, err
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Record{}, err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return Record{}, err
	}
	if err := f.Close(); err != nil {
		return Record{}, err
	}
	_ = s.remove(p.ID)
	return r, nil
}

// ---- cola ----

func (s *Store) proposalPath(id string) (string, error) {
	if !validID(id) {
		return "", fmt.Errorf("id de propuesta inválido %q", id)
	}
	rel := ".coyote/proposals/" + id + ".json"
	if err := fsx.NoSymlinks(s.Root, rel); err != nil {
		return "", err
	}
	return filepath.Join(s.Root, filepath.FromSlash(rel)), nil
}

func validID(id string) bool {
	if !strings.HasPrefix(id, "P-") || len(id) < 3 || len(id) > 40 {
		return false
	}
	for _, r := range id[2:] {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// Queue devuelve la cola, sin propuestas vencidas (que se borran).
func (s *Store) Queue() ([]Proposal, error) { return s.scan(true) }

// List devuelve la cola sin las vencidas y sin borrar nada: para quien solo
// mira, como la web.
func (s *Store) List() ([]Proposal, error) { return s.scan(false) }

func (s *Store) scan(prune bool) ([]Proposal, error) {
	entries, err := os.ReadDir(s.queueDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := fsx.NoSymlinks(s.Root, ".coyote/proposals"); err != nil {
		return nil, err
	}
	now := s.now()
	var out []Proposal
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := fsx.ReadFile(s.Root, ".coyote/proposals/"+name, 64<<20)
		if err != nil {
			continue
		}
		var p Proposal
		if json.Unmarshal(data, &p) != nil || p.ID+".json" != name {
			continue
		}
		if (p.Status == "rejected" && now.Sub(p.RejectedAt) > RejectedTTL) ||
			(p.Status != "rejected" && now.Sub(p.Last) > PendingTTL) {
			if prune {
				_ = s.remove(p.ID)
			}
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].First.Before(out[j].First) })
	return out, nil
}

// Get busca una propuesta por id o por prefijo único (con o sin P-).
func (s *Store) Get(ref string) (Proposal, error) {
	q, err := s.Queue()
	if err != nil {
		return Proposal{}, err
	}
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "P-") {
		ref = "P-" + ref
	}
	var found []Proposal
	for _, p := range q {
		if p.ID == ref {
			return p, nil
		}
		if strings.HasPrefix(p.ID, ref) {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return Proposal{}, fmt.Errorf("no hay una propuesta %s en la cola (coyote approvals la muestra)", ref)
	case 1:
		return found[0], nil
	}
	return Proposal{}, fmt.Errorf("%s coincide con %d propuestas; escribe más caracteres", ref, len(found))
}

// Save guarda una propuesta (0600: puede traer contenido del proyecto).
func (s *Store) Save(p Proposal) error {
	dst, err := s.proposalPath(p.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteAtomic(dst, b, 0o600)
}

func (s *Store) remove(id string) error {
	p, err := s.proposalPath(id)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// Enqueue agrega la propuesta o actualiza la que ya pide el mismo hash.
// Devuelve la propuesta guardada y si es nueva.
func (s *Store) Enqueue(p Proposal) (Proposal, bool, error) {
	q, err := s.Queue()
	if err != nil {
		return Proposal{}, false, err
	}
	now := s.now()
	for _, old := range q {
		if old.Hash == p.Hash {
			old.Last, old.Attempts = now, old.Attempts+1
			return old, false, s.Save(old)
		}
	}
	if p.ID == "" {
		if p.ID, err = s.NewID(); err != nil {
			return Proposal{}, false, err
		}
	}
	p.First, p.Last, p.Attempts, p.Status = now, now, 1, "pending"
	if b, _ := json.Marshal(p.Input); len(b) > maxStoredInput {
		p.Input, p.InputOmitted = nil, true
	}
	return p, true, s.Save(p)
}

// Reject marca una propuesta como rechazada, con el motivo que verá el agente.
func (s *Store) Reject(p Proposal, by, reason string) (Proposal, error) {
	p.Status, p.RejectReason, p.RejectedBy, p.RejectedAt = "rejected", reason, by, s.now()
	return p, s.Save(p)
}

// ---- lock ----

// Lock serializa el gate y las aprobaciones de un proyecto: dos llamadas en
// paralelo no pueden gastar el mismo uso.
func Lock(root string) (func(), error) {
	dir := filepath.Join(root, ".coyote")
	if err := fsx.NoSymlinks(root, ".coyote/gate.lock"); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lk := filepath.Join(dir, "gate.lock")
	for i := 0; i < 200; i++ {
		f, err := os.OpenFile(lk, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { _ = os.Remove(lk) }, nil
		}
		if info, err := os.Stat(lk); err == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(lk) // un proceso murió con el lock tomado
			continue
		}
		time.Sleep(25 * time.Millisecond)
	}
	return nil, errors.New("el gate está ocupado por otro proceso; reintenta")
}
