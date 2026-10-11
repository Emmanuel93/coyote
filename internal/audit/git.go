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

// checkAppendOnly revisa en la historia que ningún commit cambie, borre ni
// cambie de tipo un archivo del ledger, y lo mismo en el árbol de trabajo.
// Un merge se compara con su primer padre. Lo que no se puede leer cuenta
// como falla: una verificación que no corrió no puede dar ok.
func checkAppendOnly(in Input) Check {
	c := Check{Name: "el ledger solo agrega líneas", Result: OK}
	fail := func(s string) {
		c.Result = Fail
		if len(c.Detail) < 20 {
			c.Detail = append(c.Detail, s)
		}
	}
	if out, err := in.Git("rev-parse", "--is-shallow-repository"); err == nil && strings.TrimSpace(out) == "true" {
		c.Result = Warn
		c.Detail = append(c.Detail, "el clon es superficial: la historia que no bajó no se revisó")
	}
	// Las rutas, relativas a la carpeta del proyecto y sin comillas: un
	// proyecto en una subcarpeta o con acentos en su ruta se revisa igual.
	out, err := in.Git("-c", "core.quotePath=false", "log", "-m", "--first-parent", "--no-renames", "--relative",
		"--format=@%H %P", "--name-status", "--diff-filter=MDT", "--", ledgerDir)
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
		switch status {
		case "D":
			fail(fmt.Sprintf("el commit %s borra %s", short, p))
			continue
		case "T":
			fail(fmt.Sprintf("el commit %s cambia el tipo de %s (un symlink o un submódulo esconden el archivo)", short, p))
			continue
		}
		old, err1 := in.Git("cat-file", "blob", parent+":./"+p)
		nw, err2 := in.Git("cat-file", "blob", commit+":./"+p)
		switch {
		case err1 != nil || err2 != nil:
			fail(fmt.Sprintf("no pude leer %s en el commit %s", p, short))
		case !strings.HasPrefix(nw, old):
			fail(fmt.Sprintf("el commit %s cambia o borra líneas de %s", short, p))
		}
	}
	// Sin commit todavía: cada archivo de HEAD contra el disco. Se recorre
	// HEAD, no git diff: un renombre preparado o skip-worktree no esconden
	// un cambio.
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
			if !strings.HasPrefix(string(data), old) {
				fail(fmt.Sprintf("%s tiene líneas cambiadas o borradas sin commit", p))
			}
		}
	}
	return c
}

// checkBackdated busca eventos que llegaron en un commit del periodo con una
// fecha más de un día anterior a ese commit, o sin commit con una fecha más
// de un día anterior a ahora: un evento con fecha pasada cae en un mes que
// quizá ya se auditó. Una aprobación o un rechazo de un agente con fecha
// pasada es falla; lo demás, como una importación con --ts, es aviso.
func checkBackdated(in Input) Check {
	c := Check{Name: "eventos con fecha anterior a su commit", Result: OK}
	note := func(l ccf.Line, where string, landed time.Time) {
		agentDecision := (l.Type == "apr" || l.Type == "rej") && usage.Agent(l.Actor) != "" && usage.Agent(l.Actor) != "-"
		switch {
		case agentDecision:
			c.Result = Fail
		case c.Result == OK:
			c.Result = Warn
		}
		if len(c.Detail) < 20 {
			days := int(landed.Sub(l.TS).Hours() / 24)
			c.Detail = append(c.Detail, fmt.Sprintf("%s agrega un %s de %s con fecha %s, %d días antes", where, l.Type, l.Actor, l.TS.Format("2006-01-02 15:04Z"), days))
		}
	}
	scan := func(diff string, where func(string) (string, time.Time)) {
		label, landed := "", time.Time{}
		for _, line := range strings.Split(diff, "\n") {
			if strings.HasPrefix(line, "@@") {
				continue // encabezado de un trozo del diff
			}
			if rest, ok := strings.CutPrefix(line, "@"); ok {
				label, landed = where(rest)
				continue
			}
			if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") || label == "" {
				continue
			}
			l, err := ccf.Parse(strings.TrimPrefix(line, "+"))
			if err == nil && landed.Sub(l.TS) > 24*time.Hour {
				note(l, label, landed)
			}
		}
	}
	out, err := in.Git("-c", "core.quotePath=false", "log", "-m", "--first-parent", "--no-renames", "--relative", "-p", "-U0",
		"--no-ext-diff", "--no-textconv", "--since="+in.From.Format(time.RFC3339), "--until="+in.To.Format(time.RFC3339),
		"--format=@%H %ct", "--", ledgerDir)
	if err != nil {
		return Check{Name: c.Name, Result: Warn, Detail: []string{"no pude leer la historia: " + oneLine(err.Error(), 120)}}
	}
	scan(out, func(rest string) (string, time.Time) {
		f := strings.Fields(rest)
		if len(f) < 2 {
			return "", time.Time{}
		}
		var unix int64
		fmt.Sscanf(f[1], "%d", &unix)
		return "el commit " + f[0][:min(7, len(f[0]))], time.Unix(unix, 0).UTC()
	})
	// Lo que todavía no tiene commit cuenta contra ahora, si ahora cae en el periodo.
	if !in.Now.Before(in.From) && in.Now.Before(in.To) {
		if out, err := in.Git("-c", "core.quotePath=false", "diff", "--no-ext-diff", "--no-textconv", "--relative", "-U0", "HEAD", "--", ledgerDir); err == nil {
			scan("@\n"+out, func(string) (string, time.Time) { return "un cambio sin commit", in.Now })
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
