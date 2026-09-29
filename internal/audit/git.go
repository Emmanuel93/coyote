package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/usage"
)

const ledgerDir = "coyote/ledger"

// checkAppendOnly revisa en la historia que ningún commit cambie ni borre
// líneas del ledger, y lo mismo en el árbol de trabajo. Un merge se compara
// con su primer padre.
func checkAppendOnly(in Input) Check {
	c := Check{Name: "el ledger solo agrega líneas", Result: OK}
	fail := func(s string) {
		c.Result = Fail
		c.Detail = append(c.Detail, s)
	}
	out, err := in.Git("log", "-m", "--first-parent", "--no-renames", "--format=@%H %P", "--name-status", "--diff-filter=MD", "--", ledgerDir)
	if err != nil {
		return Check{Name: c.Name, Result: Warn, Detail: []string{"no pude leer la historia: " + oneLine(err.Error(), 120)}}
	}
	commit, parent := "", ""
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "@"); ok {
			f := strings.Fields(rest)
			commit, parent = "", ""
			if len(f) >= 2 {
				commit, parent = f[0], f[1]
			}
			continue
		}
		status, p, ok := strings.Cut(line, "\t")
		if !ok || commit == "" {
			continue
		}
		short := commit[:min(7, len(commit))]
		if status == "D" {
			fail(fmt.Sprintf("el commit %s borra %s", short, p))
			continue
		}
		old, err1 := in.Git("cat-file", "-p", parent+":"+p)
		nw, err2 := in.Git("cat-file", "-p", commit+":"+p)
		if err1 == nil && err2 == nil && !strings.HasPrefix(nw, old) {
			fail(fmt.Sprintf("el commit %s cambia o borra líneas de %s", short, p))
		}
	}
	// Sin commit todavía: el archivo contra HEAD.
	if out, err := in.Git("diff", "--no-ext-diff", "--no-textconv", "--name-status", "HEAD", "--", ledgerDir); err == nil {
		for _, line := range strings.Split(out, "\n") {
			status, p, ok := strings.Cut(line, "\t")
			if !ok {
				continue
			}
			if status == "D" {
				fail(fmt.Sprintf("%s se borró y no tiene commit", p))
				continue
			}
			old, err1 := in.Git("cat-file", "-p", "HEAD:"+p)
			data, err2 := os.ReadFile(filepath.Join(in.Root, filepath.FromSlash(p)))
			if err1 == nil && err2 == nil && !strings.HasPrefix(string(data), old) {
				fail(fmt.Sprintf("%s tiene líneas cambiadas o borradas sin commit", p))
			}
		}
	}
	return c
}

// checkActors cruza el actor de cada línea con la persona del archivo y con
// quien hizo el commit que la trajo. La identidad es la que declara la
// máquina: el cruce encuentra incoherencias, no prueba identidades.
func checkActors(in Input, period []ledger.Entry) Check {
	c := Check{Name: "actor, archivo y autor del commit coinciden", Result: OK}
	warn := func(s string) {
		if c.Result == OK {
			c.Result = Warn
		}
		if len(c.Detail) < 20 {
			c.Detail = append(c.Detail, s)
		}
	}
	files := map[string][]ledger.Entry{}
	for _, e := range period {
		files[e.File] = append(files[e.File], e)
	}
	names := make([]string, 0, len(files))
	for f := range files {
		names = append(names, f)
	}
	sort.Strings(names)
	persons := map[string]map[string]bool{} // correo del autor -> personas del ledger
	uncommitted := 0
	for _, f := range names {
		rp := rel(in.Root, f)
		owner := filePerson(f)
		authors := blameAuthors(in.Git, rp)
		for _, e := range files[f] {
			person := usage.Person(e.Line.Actor)
			if owner != "" && person != owner {
				warn(fmt.Sprintf("%s:%d: línea de %s en el archivo de %s", rp, e.LineNo, person, owner))
			}
			mail, ok := authors[e.LineNo]
			if !ok {
				uncommitted++
				continue
			}
			if persons[mail] == nil {
				persons[mail] = map[string]bool{}
			}
			persons[mail][person] = true
		}
	}
	mails := make([]string, 0, len(persons))
	for m := range persons {
		mails = append(mails, m)
	}
	sort.Strings(mails)
	for _, m := range mails {
		if len(persons[m]) > 1 {
			var ps []string
			for p := range persons[m] {
				ps = append(ps, p)
			}
			sort.Strings(ps)
			warn(fmt.Sprintf("el autor de git %s registró a nombre de %s", m, strings.Join(ps, ", ")))
		}
	}
	if uncommitted > 0 {
		c.Detail = append(c.Detail, fmt.Sprintf("%d líneas todavía sin commit no se cruzaron", uncommitted))
	}
	return c
}

// blameAuthors devuelve el correo del autor del commit que trajo cada línea
// del archivo, en su versión del árbol de trabajo.
func blameAuthors(git Git, rp string) map[int]string {
	out, err := git("blame", "--line-porcelain", "--", rp)
	if err != nil {
		return nil
	}
	authors := map[int]string{}
	line, mail, zero := 0, "", false
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "\t"):
			if line > 0 && !zero {
				authors[line] = mail
			}
			line, mail = 0, ""
		case strings.HasPrefix(l, "author-mail "):
			mail = strings.Trim(strings.TrimPrefix(l, "author-mail "), "<>")
		default:
			f := strings.Fields(l)
			if len(f) >= 3 && len(f[0]) >= 40 && isHex(f[0]) {
				fmt.Sscanf(f[2], "%d", &line)
				zero = strings.Trim(f[0], "0") == ""
			}
		}
	}
	return authors
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// checkCommits revisa los commits del periodo: atribución a IA (R15) y
// cuántos van firmados.
func checkCommits(in Input) (Check, int, int) {
	c := Check{Name: "commits del periodo sin atribución a IA (R15)", Result: OK}
	out, err := in.Git("log", "--since="+in.From.Format(time.RFC3339), "--until="+in.To.Format(time.RFC3339),
		"--format=%H%x00%G?%x00%B%x1e")
	if err != nil {
		return Check{Name: c.Name, Result: Warn, Detail: []string{"no pude leer los commits: " + oneLine(err.Error(), 120)}}, 0, 0
	}
	commits, signed := 0, 0
	for _, rec := range strings.Split(out, "\x1e") {
		parts := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x00", 3)
		if len(parts) < 3 || parts[0] == "" {
			continue
		}
		commits++
		if parts[1] != "N" {
			signed++
		}
		if in.Attribution != nil {
			if f := in.Attribution.Check(parts[2], false); len(f) > 0 {
				c.Result = Fail
				c.Detail = append(c.Detail, fmt.Sprintf("%s lleva atribución a una herramienta de IA", parts[0][:min(7, len(parts[0]))]))
			}
		}
	}
	return c, commits, signed
}
