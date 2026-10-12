package audit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitRepo corre git en dir con identidad y fecha fijas y sin firmar.
func gitRepo(t *testing.T, dir string, date time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false", "-c", "user.name=Ana", "-c", "user.email=ana@example.com"}, args...)...)
	stamp := date.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestHistoriaAnteriorSinUnProcesoPorMerge: leer la historia anterior al
// periodo cuesta lo mismo con dos merges que con diez. Un proceso de git por
// merge hacía que auditar un repo grande tardara minutos.
func TestHistoriaAnteriorSinUnProcesoPorMerge(t *testing.T) {
	count := func(merges int) (calls int, prior []ledgerDiff) {
		dir := t.TempDir()
		old := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
		gitRepo(t, dir, old, "init", "-q", "-b", "main")
		ledger := filepath.Join(dir, "coyote", "ledger", "2020", "01")
		if err := os.MkdirAll(ledger, 0o755); err != nil {
			t.Fatal(err)
		}
		main := filepath.Join(ledger, "01-ana.ccf")
		if err := os.WriteFile(main, []byte("linea 0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRepo(t, dir, old, "add", "-A")
		gitRepo(t, dir, old, "commit", "-qm", "base")
		for i := 1; i <= merges; i++ {
			day := old.AddDate(0, 0, i)
			gitRepo(t, dir, day, "checkout", "-q", "-b", fmt.Sprintf("lado%d", i))
			if err := os.WriteFile(filepath.Join(ledger, fmt.Sprintf("%02d-ben.ccf", i+1)), []byte("lado\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitRepo(t, dir, day, "add", "-A")
			gitRepo(t, dir, day, "commit", "-qm", "lado")
			gitRepo(t, dir, day, "checkout", "-q", "main")
			body, _ := os.ReadFile(main)
			if err := os.WriteFile(main, append(body, []byte(fmt.Sprintf("linea %d\n", i))...), 0o644); err != nil {
				t.Fatal(err)
			}
			gitRepo(t, dir, day, "commit", "-qam", "main")
			gitRepo(t, dir, day, "merge", "-q", "--no-ff", "-m", "merge", fmt.Sprintf("lado%d", i))
		}
		// Un merge malo: quita la última línea de main frente a su primer padre.
		gitRepo(t, dir, old.AddDate(0, 0, 400), "checkout", "-q", "-b", "malo")
		gitRepo(t, dir, old.AddDate(0, 0, 400), "commit", "-q", "--allow-empty", "-m", "malo")
		gitRepo(t, dir, old.AddDate(0, 0, 401), "checkout", "-q", "main")
		gitRepo(t, dir, old.AddDate(0, 0, 401), "merge", "-q", "--no-ff", "--no-commit", "malo")
		body, _ := os.ReadFile(main)
		lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
		if err := os.WriteFile(main, []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRepo(t, dir, old.AddDate(0, 0, 402), "commit", "-qam", "merge malo")
		now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
		gitRepo(t, dir, now.AddDate(0, 0, -6), "commit", "-q", "--allow-empty", "-m", "en el periodo")
		in := Input{Root: dir, From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Now: now,
			Git: func(args ...string) (string, error) {
				calls++
				out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
				return string(out), err
			}}
		h, err := readHistory(in)
		if err != nil {
			t.Fatal(err)
		}
		return calls, h.prior
	}
	few, priorFew := count(2)
	many, priorMany := count(10)
	if few != many {
		t.Errorf("los procesos de git dependen de los merges: %d con 2, %d con 10", few, many)
	}
	// Solo el merge que quita una línea se avisa, contra cada uno de sus dos padres.
	for _, prior := range [][]ledgerDiff{priorFew, priorMany} {
		if len(prior) != 2 || prior[0].commit != prior[1].commit || !prior[0].merge || prior[0].parent == prior[1].parent {
			t.Errorf("se esperaba el merge que quita una línea frente a sus dos padres: %+v", prior)
		}
	}
}
