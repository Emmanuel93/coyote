package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/usage"
)

const ledgerDir = "coyote/ledger"

// gitCommit es un commit de la historia de HEAD.
type gitCommit struct {
	sha     string
	parents []string
	when    time.Time // fecha del commit
}

// ledgerDiff es lo que un commit cambia en el ledger frente a uno de sus
// padres: líneas quitadas por archivo y líneas agregadas.
type ledgerDiff struct {
	commit, parent string
	merge          bool
	removed        map[string]int
	added          []string
}

// history es la historia del ledger que leen las verificaciones.
type history struct {
	commits []gitCommit
	byID    map[string]gitCommit
	diffs   []ledgerDiff
	shallow bool
}

// readHistory recorre todos los commits de HEAD, no solo la cadena de
// primeros padres: un merge cuyo primer padre ya no tiene una línea, seguido
// de un fast-forward, también se ve. Cada commit se compara con cada uno de
// sus padres, con el diff en texto (--text) para que un .gitattributes con
// -diff no lo esconda.
func readHistory(in Input) (*history, error) {
	h := &history{byID: map[string]gitCommit{}}
	if out, err := in.Git("rev-parse", "--is-shallow-repository"); err == nil && strings.TrimSpace(out) == "true" {
		h.shallow = true
	}
	out, err := in.Git("log", "--format=%H %P%x00%ct", "HEAD")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		ids, ct, ok := strings.Cut(line, "\x00")
		f := strings.Fields(ids)
		if !ok || len(f) == 0 {
			continue
		}
		var unix int64
		fmt.Sscanf(ct, "%d", &unix)
		c := gitCommit{sha: f[0], parents: f[1:], when: time.Unix(unix, 0).UTC()}
		h.commits = append(h.commits, c)
		h.byID[c.sha] = c
	}
	// Los commits que tocan el ledger frente a algún padre, sin simplificar
	// la historia.
	out, err = in.Git("log", "--full-history", "-m", "--no-renames", "--relative", "--format=@%H", "--name-only", "--", ledgerDir)
	if err != nil {
		return nil, err
	}
	touched := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if sha, ok := strings.CutPrefix(line, "@"); ok {
			touched[strings.TrimSpace(sha)] = true
		}
	}
	for _, c := range h.commits {
		if !touched[c.sha] {
			continue
		}
		parents := c.parents
		if len(parents) == 0 {
			parents = []string{""} // el commit raíz, contra el árbol vacío
		}
		for _, p := range parents {
			args := []string{"-c", "core.quotePath=false", "diff-tree", "-p", "--text", "-U0", "--no-ext-diff", "--no-textconv", "--no-renames", "--relative"}
			if p == "" {
				args = append(args, "--root", c.sha)
			} else {
				args = append(args, p, c.sha)
			}
			out, err := in.Git(append(args, "--", ledgerDir)...)
			if err != nil {
				return nil, err
			}
			d := ledgerDiff{commit: c.sha, parent: p, merge: len(c.parents) > 1, removed: map[string]int{}}
			file := ""
			for _, l := range strings.Split(out, "\n") {
				switch {
				case strings.HasPrefix(l, "diff --git "):
					file = ""
				case strings.HasPrefix(l, "--- a/"):
					file = strings.TrimPrefix(l, "--- a/")
				case strings.HasPrefix(l, "+++ b/"):
					if file == "" {
						file = strings.TrimPrefix(l, "+++ b/")
					}
				case strings.HasPrefix(l, "---"), strings.HasPrefix(l, "+++"):
				case strings.HasPrefix(l, "-"):
					d.removed[file]++
				case strings.HasPrefix(l, "+"):
					d.added = append(d.added, strings.TrimPrefix(l, "+"))
				}
			}
			h.diffs = append(h.diffs, d)
		}
	}
	return h, nil
}

// checkAppendOnly revisa que ningún commit cambie ni borre líneas del ledger
// frente a ninguno de sus padres, y lo mismo en el árbol de trabajo. Lo que
// no se puede leer cuenta como falla: una verificación que no corrió no
// puede dar ok.
func checkAppendOnly(in Input, h *history, herr error) Check {
	c := Check{Name: "el ledger solo agrega líneas", Result: OK}
	fail := func(s string) {
		c.Result = Fail
		if len(c.Detail) < 20 {
			c.Detail = append(c.Detail, s)
		}
	}
	if herr != nil {
		return Check{Name: c.Name, Result: Warn, Detail: []string{"no pude leer la historia: " + oneLine(herr.Error(), 120)}}
	}
	if h.shallow {
		c.Result = Warn
		c.Detail = append(c.Detail, "el clon es superficial: la historia que no bajó no se revisó")
	}
	for _, d := range h.diffs {
		for f, n := range d.removed {
			if n == 0 {
				continue
			}
			against := ""
			if d.merge {
				against = " frente a su padre " + d.parent[:min(7, len(d.parent))]
			}
			fail(fmt.Sprintf("el commit %s cambia, borra o esconde líneas de %s%s", d.commit[:min(7, len(d.commit))], f, against))
		}
	}
	// Sin commit todavía: cada archivo de HEAD contra el disco. Se recorre
	// HEAD, no git diff: un renombre preparado o skip-worktree no esconden
	// un cambio. Un checkout con CRLF (eol=crlf) no cuenta como cambio.
	if out, err := in.Git("-c", "core.quotePath=false", "ls-tree", "-r", "-z", "--name-only", "HEAD", "--", ledgerDir); err == nil {
		for _, p := range strings.Split(out, "\x00") {
			if p == "" {
				continue
			}
			old, err := in.Git("cat-file", "blob", "HEAD:./"+p)
			if err != nil {
				fail(fmt.Sprintf("no pude leer %s en HEAD", p))
				continue
			}
			disk := filepath.Join(in.Root, filepath.FromSlash(p))
			info, err := os.Lstat(disk)
			switch {
			case os.IsNotExist(err):
				fail(fmt.Sprintf("%s se borró o se movió y no tiene commit", p))
				continue
			case err != nil || !info.Mode().IsRegular():
				fail(fmt.Sprintf("%s ya no es un archivo y no tiene commit", p))
				continue
			}
			data, err := os.ReadFile(disk)
			if err != nil {
				fail(fmt.Sprintf("no pude leer %s", p))
				continue
			}
			crlf := strings.NewReplacer("\r\n", "\n")
			if !strings.HasPrefix(crlf.Replace(string(data)), crlf.Replace(old)) {
				fail(fmt.Sprintf("%s tiene líneas cambiadas o borradas sin commit", p))
			}
		}
	}
	return c
}

// checkBackdated busca eventos con una fecha más de un día anterior al
// commit que los trajo (el que no es un merge: lo que llega por un merge se
// mide contra el commit de su rama) o, sin commit, anterior a ahora. Una
// aprobación o un rechazo de un agente que cae en un mes anterior al de su
// commit, quizá ya auditado, es falla; lo demás, como una importación con
// --ts o un squash, es aviso. También avisa de commits con fecha anterior a
// la de su padre: un reloj atrasado o una fecha cambiada.
func checkBackdated(in Input, h *history, herr error) Check {
	c := Check{Name: "eventos con fecha anterior a su commit", Result: OK}
	if herr != nil {
		return Check{Name: c.Name, Result: Warn, Detail: []string{"no pude leer la historia: " + oneLine(herr.Error(), 120)}}
	}
	warn := func(s string) {
		if c.Result == OK {
			c.Result = Warn
		}
		if len(c.Detail) < 20 {
			c.Detail = append(c.Detail, s)
		}
	}
	note := func(l ccf.Line, where string, landed time.Time) {
		days := int(landed.Sub(l.TS).Hours() / 24)
		msg := fmt.Sprintf("%s agrega un %s de %s con fecha %s, %d días antes", where, l.Type, l.Actor, l.TS.Format("2006-01-02 15:04Z"), days)
		agentDecision := (l.Type == "apr" || l.Type == "rej") && usage.Agent(l.Actor) != "" && usage.Agent(l.Actor) != "-"
		if agentDecision && l.TS.Format("2006-01") < landed.Format("2006-01") {
			c.Result = Fail
			if len(c.Detail) < 20 {
				c.Detail = append(c.Detail, msg+": cae en un mes anterior")
			}
			return
		}
		warn(msg)
	}
	inPeriod := func(t time.Time) bool { return !t.Before(in.From) && t.Before(in.To) }
	for _, d := range h.diffs {
		cm := h.byID[d.commit]
		if d.merge || !inPeriod(cm.when) {
			continue
		}
		for _, line := range d.added {
			if l, err := ccf.Parse(line); err == nil && cm.when.Sub(l.TS) > 24*time.Hour {
				note(l, "el commit "+cm.sha[:min(7, len(cm.sha))], cm.when)
			}
		}
	}
	for _, cm := range h.commits {
		for _, p := range cm.parents {
			pc, ok := h.byID[p]
			if !ok || !inPeriod(cm.when) && !inPeriod(pc.when) {
				continue
			}
			if pc.when.Sub(cm.when) > time.Hour {
				warn(fmt.Sprintf("el commit %s tiene fecha anterior a la de su padre %s: un reloj atrasado o una fecha cambiada", cm.sha[:min(7, len(cm.sha))], p[:min(7, len(p))]))
			}
		}
	}
	// Lo que todavía no tiene commit cuenta contra ahora, si ahora cae en el periodo.
	if inPeriod(in.Now) {
		if out, err := in.Git("-c", "core.quotePath=false", "diff", "--text", "--no-ext-diff", "--no-textconv", "--relative", "-U0", "HEAD", "--", ledgerDir); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
					continue
				}
				if l, err := ccf.Parse(strings.TrimPrefix(line, "+")); err == nil && in.Now.Sub(l.TS) > 24*time.Hour {
					note(l, "un cambio sin commit", in.Now)
				}
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
	// Sin --since ni --until: git deja de recorrer en el primer commit con
	// fecha vieja, y uno así escondería los anteriores. La fecha se filtra aquí.
	out, err := in.Git("log", "--format=%H%x00%ct%x00%G?%x00%B%x1e", "HEAD")
	if err != nil {
		return Check{Name: c.Name, Result: Warn, Detail: []string{"no pude leer los commits: " + oneLine(err.Error(), 120)}}, 0, 0
	}
	commits, signed := 0, 0
	for _, rec := range strings.Split(out, "\x1e") {
		f := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x00", 4)
		if len(f) < 4 || f[0] == "" {
			continue
		}
		var unix int64
		fmt.Sscanf(f[1], "%d", &unix)
		if when := time.Unix(unix, 0).UTC(); when.Before(in.From) || !when.Before(in.To) {
			continue
		}
		parts := []string{f[0], f[2], f[3]}
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
