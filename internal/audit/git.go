package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
// padres: líneas quitadas (que no vuelven a aparecer) por archivo y líneas
// agregadas.
type ledgerDiff struct {
	commit, parent string
	merge          bool
	removed        map[string]int
	added          []string
}

// history es la historia del ledger que leen las verificaciones. Un commit
// cuenta para el periodo en que llegó a la cadena de primeros padres de
// HEAD, no por su fecha: una rama con fechas viejas, o un commit con la
// fecha cambiada, llega cuando se integra.
type history struct {
	byID    map[string]gitCommit
	chain   []gitCommit          // primeros padres, de la raíz a HEAD
	landed  map[string]time.Time // fecha de llegada de cada commit de la cadena
	arrived []gitCommit          // los commits que llegaron en el periodo, de cualquier rama
	diffs   []ledgerDiff         // de los commits que llegaron, contra cada padre
	prior   []ledgerDiff         // de los que llegaron antes del periodo y borran o cambian líneas
	shallow bool
	skewed  []string // commits de la cadena con fecha anterior a la de su padre o futura
}

// readHistory lee la historia de HEAD para el periodo. Los diffs van en
// texto (--text), así que un .gitattributes con -diff no los esconde, y sin
// --since, que deja de recorrer en el primer commit con fecha vieja.
func readHistory(in Input) (*history, error) {
	h := &history{byID: map[string]gitCommit{}, landed: map[string]time.Time{}}
	if out, err := in.Git("rev-parse", "--is-shallow-repository"); err == nil && strings.TrimSpace(out) == "true" {
		h.shallow = true
	}
	out, err := in.Git("log", "--format=%H %P%x00%ct", "HEAD")
	if err != nil {
		return nil, err
	}
	head := ""
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		ids, ct, ok := strings.Cut(line, "\x00")
		f := strings.Fields(ids)
		if !ok || len(f) == 0 {
			continue
		}
		var unix int64
		fmt.Sscanf(ct, "%d", &unix)
		c := gitCommit{sha: f[0], parents: f[1:], when: time.Unix(unix, 0).UTC()}
		if head == "" {
			head = c.sha
		}
		h.byID[c.sha] = c
	}
	for sha := head; sha != ""; {
		c, ok := h.byID[sha]
		if !ok {
			break
		}
		h.chain = append([]gitCommit{c}, h.chain...)
		sha = ""
		if len(c.parents) > 0 {
			sha = c.parents[0]
		}
	}
	// La fecha de llegada no retrocede: un commit con la fecha cambiada hacia
	// atrás llega cuando llega su predecesor en la cadena. Y una fecha
	// imposible (posterior a ahora) no cuenta: ese commit llega con el que le
	// sigue, o ahora si es el último. Así un commit con fecha de 2099 no
	// arrastra a los que vienen después fuera de su mes, que es lo que haría
	// recortarla a ahora: al auditar un mes cerrado, esos commits quedarían
	// fuera de todo reporte.
	at := make([]time.Time, len(h.chain))
	impossible := make([]bool, len(h.chain))
	var prev time.Time
	for i, c := range h.chain {
		if c.when.After(in.Now.Add(time.Hour)) {
			impossible[i] = true
			continue
		}
		at[i] = c.when
		if at[i].After(in.Now) {
			at[i] = in.Now // un reloj apenas adelantado: ahora
		}
		if at[i].Before(prev) {
			at[i] = prev
		}
		prev = at[i]
	}
	next := in.Now
	for i := len(h.chain) - 1; i >= 0; i-- {
		if impossible[i] {
			at[i] = next
		} else {
			next = at[i]
		}
	}
	var prevWhen time.Time
	var prevFuture bool
	var start, end string
	for i, c := range h.chain {
		if i > 0 && prevWhen.Sub(c.when) > time.Hour && !prevFuture {
			h.skewed = append(h.skewed, fmt.Sprintf("el commit %s tiene fecha anterior a la de su padre: un reloj atrasado o una fecha cambiada", short(c.sha)))
		}
		if impossible[i] {
			h.skewed = append(h.skewed, fmt.Sprintf("el commit %s tiene fecha futura (%s): cuenta como llegado con el commit que le sigue, o ahora si es el último; no se sabe cuándo llegó", short(c.sha), c.when.Format("2006-01-02")))
		}
		h.landed[c.sha], prevWhen, prevFuture = at[i], c.when, impossible[i]
		if at[i].Before(in.From) {
			start = c.sha
		}
		if at[i].Before(in.To) {
			end = c.sha
		}
	}
	if start != "" {
		if err := h.readPrior(in, start); err != nil {
			return nil, err
		}
	}
	if end == "" || end == start {
		return h, nil
	}
	args := []string{"rev-list", end}
	if start != "" {
		args = append(args, "^"+start)
	}
	out, err = in.Git(args...)
	if err != nil {
		return nil, err
	}
	for _, sha := range strings.Fields(out) {
		if c, ok := h.byID[sha]; ok {
			h.arrived = append(h.arrived, c)
		}
	}
	for _, c := range h.arrived {
		parents := c.parents
		if len(parents) == 0 {
			parents = []string{""} // el commit raíz, contra el árbol vacío
		}
		for _, p := range parents {
			d, err := diffAgainst(in, c, p)
			if err != nil {
				return nil, err
			}
			h.diffs = append(h.diffs, d)
		}
	}
	return h, nil
}

// diffAgainst lee lo que un commit cambia en el ledger frente a uno de sus
// padres ("" es el árbol vacío, para el commit raíz).
func diffAgainst(in Input, c gitCommit, parent string) (ledgerDiff, error) {
	args := []string{"-c", "core.quotePath=false", "diff-tree", "-p", "--text", "-U0", "--no-ext-diff", "--no-textconv", "--no-renames", "--relative"}
	if parent == "" {
		args = append(args, "--root", c.sha)
	} else {
		args = append(args, parent, c.sha)
	}
	out, err := in.Git(append(args, "--", ledgerDir)...)
	if err != nil {
		return ledgerDiff{}, err
	}
	return parseLedgerDiff(out, c.sha, parent, len(c.parents) > 1), nil
}

// priorHeader es la línea que abre cada commit de readPrior: el hash completo
// y, en un merge, el padre contra el que va el diff que sigue.
var priorHeader = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})(?: \(from ([0-9a-f]{40}|[0-9a-f]{64})\))?(?: |$)`)

// readPrior busca en la historia que llegó antes del periodo (todo lo que
// alcanza start) los commits que borran o cambian líneas del ledger. Un
// reporte de un mes anterior pudo no verlos, o la historia pudo reescribirse
// después con fechas viejas: se avisan con el mes en que llegaron, no son
// falla del periodo. Sale de un solo git log: con -m un merge trae un diff
// contra cada padre, y la línea "hash (from padre)" dice cuál. Los prefijos
// a/ y b/ van explícitos: diff.noprefix o diff.mnemonicPrefix de la
// configuración los cambiarían y los archivos quedarían sin nombre.
func (h *history) readPrior(in Input, start string) error {
	out, err := in.Git("-c", "core.quotePath=false", "log", "--full-history", "-m", "--format=oneline", "--no-abbrev-commit", "--no-color",
		"-p", "--text", "-U0", "--no-ext-diff", "--no-textconv", "--no-renames", "--relative", "--src-prefix=a/", "--dst-prefix=b/", start, "--", ledgerDir)
	if err != nil {
		return err
	}
	var sha, from string
	var diff []string
	flush := func() {
		c, ok := h.byID[sha]
		if !ok {
			return
		}
		parent := from
		if parent == "" && len(c.parents) > 0 {
			parent = c.parents[0]
		}
		if d := parseLedgerDiff(strings.Join(diff, "\n"), c.sha, parent, len(c.parents) > 1); len(d.removed) > 0 {
			h.prior = append(h.prior, d)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if m := priorHeader.FindStringSubmatch(l); m != nil {
			flush()
			sha, from, diff = m[1], m[2], nil
			continue
		}
		diff = append(diff, l)
	}
	flush()
	return nil
}

// parseLedgerDiff lee un diff -U0 del ledger. Una línea quitada que vuelve a
// aparecer en el mismo archivo no cuenta: así se ve una línea a la que solo
// se le agregó el salto final, o una que cambió de lugar.
func parseLedgerDiff(out, commit, parent string, merge bool) ledgerDiff {
	d := ledgerDiff{commit: commit, parent: parent, merge: merge, removed: map[string]int{}}
	removed := map[string]map[string]int{}
	added := map[string]map[string]int{}
	file := ""
	bump := func(m map[string]map[string]int, line string) {
		if m[file] == nil {
			m[file] = map[string]int{}
		}
		m[file][line]++
	}
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
			bump(removed, strings.TrimPrefix(l, "-"))
		case strings.HasPrefix(l, "+"):
			bump(added, strings.TrimPrefix(l, "+"))
			d.added = append(d.added, strings.TrimPrefix(l, "+"))
		}
	}
	for f, lines := range removed {
		for line, n := range lines {
			if gone := n - added[f][line]; gone > 0 {
				d.removed[f] += gone
			}
		}
	}
	return d
}

func short(sha string) string { return sha[:min(7, len(sha))] }

// appendsTo dice si nw solo agrega líneas a old. Si old no termina en salto
// de línea, coyote se lo agrega antes de la línea nueva.
func appendsTo(old, nw string) bool {
	if !strings.HasPrefix(nw, old) {
		return false
	}
	return strings.HasSuffix(old, "\n") || old == "" || len(nw) == len(old) || nw[len(old)] == '\n'
}

// checkAppendOnly revisa que ningún commit que llegó en el periodo cambie ni
// borre líneas del ledger frente a ninguno de sus padres, y que el árbol de
// trabajo no las cambie frente a HEAD. Lo que no se puede leer cuenta como
// falla: una verificación que no corrió no puede dar ok.
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
	touched := func(d ledgerDiff) (files []string, against string) {
		for f := range d.removed {
			files = append(files, f)
		}
		sort.Strings(files)
		if d.merge {
			against = " frente a su padre " + short(d.parent)
		}
		return files, against
	}
	for _, d := range h.diffs {
		files, against := touched(d)
		for _, f := range files {
			fail(fmt.Sprintf("el commit %s cambia, borra o esconde líneas de %s%s", short(d.commit), f, against))
		}
	}
	// La historia que llegó antes del periodo no es falla de este mes, pero un
	// reporte anterior pudo no verla o pudo reescribirse después: se avisa.
	for _, d := range h.prior {
		when := "con fecha " + h.byID[d.commit].when.Format("2006-01")
		if t, ok := h.landed[d.commit]; ok {
			when = "llegó en " + t.Format("2006-01")
		}
		files, against := touched(d)
		for _, f := range files {
			if c.Result == OK {
				c.Result = Warn
			}
			if len(c.Detail) < 20 {
				c.Detail = append(c.Detail, fmt.Sprintf("el commit %s (%s, antes del periodo) cambia, borra o esconde líneas de %s%s", short(d.commit), when, f, against))
			}
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
			if !appendsTo(crlf.Replace(old), crlf.Replace(string(data))) {
				fail(fmt.Sprintf("%s tiene líneas cambiadas o borradas sin commit", p))
			}
		}
	}
	return c
}

// checkBackdated busca eventos con una fecha más de un día anterior al
// momento en que llegaron a la cadena de primeros padres de HEAD (lo que
// trae un merge cuenta en el merge), o, sin commit, anterior a ahora. Una
// aprobación o un rechazo de un agente que cae en un mes anterior al de su
// llegada, quizá ya auditado, es falla; lo demás, como una importación con
// --ts o un squash, es aviso. También avisa de commits con fecha anterior a
// la de su padre o futura.
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
		landed, onChain := h.landed[d.commit]
		cm := h.byID[d.commit]
		// Cada línea se mide en el commit de la cadena que la trajo, contra
		// su primer padre: lo que llega por un merge, en el merge.
		if !onChain || !inPeriod(landed) || len(cm.parents) > 0 && d.parent != cm.parents[0] {
			continue
		}
		for _, line := range d.added {
			if l, err := ccf.Parse(line); err == nil && landed.Sub(l.TS) > 24*time.Hour {
				note(l, "el commit "+short(d.commit), landed)
			}
		}
	}
	for _, s := range h.skewed {
		warn(s)
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
	// Sin textconv: git blame corre el que declare la configuración del repo.
	out, err := git("blame", "--no-textconv", "--line-porcelain", "--", rp)
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

// checkCommits revisa los commits que llegaron en el periodo, de cualquier
// rama: atribución a IA (R15) y cuántos van firmados.
func checkCommits(in Input, h *history, herr error) (Check, int, int) {
	c := Check{Name: "commits del periodo sin atribución a IA (R15)", Result: OK}
	if herr != nil {
		return Check{Name: c.Name, Result: Warn, Detail: []string{"no pude leer la historia: " + oneLine(herr.Error(), 120)}}, 0, 0
	}
	commits, signed := 0, 0
	fail := func(s string) {
		c.Result = Fail
		if len(c.Detail) < 20 {
			c.Detail = append(c.Detail, s)
		}
	}
	for _, cm := range h.arrived {
		out, err := in.Git("cat-file", "commit", cm.sha)
		if err != nil {
			return Check{Name: c.Name, Result: Warn, Detail: []string{"no pude leer los commits: " + oneLine(err.Error(), 120)}}, 0, 0
		}
		sig, enc, msg := splitCommit(out)
		commits++
		if sig {
			signed++
		}
		if in.Attribution == nil {
			continue
		}
		// Los bytes crudos, y el mensaje como lo lee quien lo ve: con otra
		// codificación (encoding IBM037, por ejemplo) el trailer no aparece en
		// los bytes y git sí lo muestra. Un mensaje con bytes nulos no es el de
		// un commit de git y no se puede revisar.
		texts := []string{msg}
		switch {
		case strings.ContainsRune(msg, 0):
			fail(fmt.Sprintf("%s tiene bytes nulos en el mensaje: no se puede revisar su atribución", short(cm.sha)))
		case enc != "" && !isUTF8(enc):
			conv, err := in.Git("log", "-1", "--format=%B", "--encoding=UTF-8", cm.sha)
			if err != nil {
				if c.Result == OK {
					c.Result = Warn
				}
				c.Detail = append(c.Detail, fmt.Sprintf("%s está en la codificación %s y no pude convertir su mensaje a UTF-8", short(cm.sha), oneLine(enc, 40)))
				break
			}
			texts = append(texts, conv)
		}
		for _, text := range texts {
			if f := in.Attribution.Check(text, false); len(f) > 0 {
				fail(fmt.Sprintf("%s lleva atribución a una herramienta de IA", short(cm.sha)))
				break
			}
		}
	}
	return c, commits, signed
}

// isUTF8 dice si la etiqueta del encabezado encoding de un commit es UTF-8.
func isUTF8(label string) bool {
	return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(strings.TrimSpace(label))) == "utf8"
}

// splitCommit separa un commit crudo en si lleva firma, su codificación
// declarada (el encabezado encoding, vacío si no hay) y su mensaje. La firma
// se cuenta, no se verifica: verificarla corre gpg, ssh-keygen o el programa
// que declare la configuración del repo, y auditar no ejecuta nada ajeno.
// Quien necesite la verificación la corre con git verify-commit y sus llaves.
func splitCommit(raw string) (signed bool, encoding, msg string) {
	head, msg, _ := strings.Cut(raw, "\n\n")
	for _, l := range strings.Split(head, "\n") {
		switch {
		case strings.HasPrefix(l, "gpgsig "), strings.HasPrefix(l, "gpgsig-sha256 "):
			signed = true
		case strings.HasPrefix(l, "encoding "):
			encoding = strings.TrimPrefix(l, "encoding ")
		}
	}
	return signed, encoding, msg
}
