package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Emmanuel93/coyote/internal/attribution"
	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/ccfdoc"
	"github.com/Emmanuel93/coyote/internal/gitx"
	"github.com/Emmanuel93/coyote/internal/glob"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/standards"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, "\n\n") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

var (
	convRe  = regexp.MustCompile(`^([a-z]+)(?:\(([^)]+)\))?!?:\s*(.+)$`)
	idClean = regexp.MustCompile(`[^\p{L}\p{N}._/-]+`)
)

// ledgerID deja un valor apto para los campos de identificador del ledger.
func ledgerID(s string) string {
	s = strings.Trim(idClean.ReplaceAllString(strings.TrimSpace(s), "-"), "-._/")
	if s == "" {
		return "-"
	}
	return s
}

func parseConventional(subject string) (typ, scope, desc string) {
	m := convRe.FindStringSubmatch(strings.TrimSpace(subject))
	if m == nil {
		return "chore", "-", subject
	}
	typ = m[1]
	switch typ {
	case "docs":
		typ = "doc"
	case "style":
		typ = "chore"
	case "revert":
		typ = "fix"
	}
	if _, ok := ccf.Types[typ]; !ok {
		typ = "chore"
	}
	scope = "-"
	if m[2] != "" {
		scope = ledgerID(m[2])
	}
	return typ, scope, m[3]
}

func cmdCommit(a *app, args []string) error {
	fs := a.flags("commit", "-m <mensaje> [-a] [--ws W] [--agent A]")
	var msgs multiFlag
	fs.Var(&msgs, "m", "mensaje; repetible como en git")
	all := fs.Bool("a", false, "incluye cambios de archivos versionados, como git commit -a")
	noVerify := fs.Bool("no-verify", false, "omite R2 y R14 (la limpieza de atribución y la revisión de autoría no se omiten)")
	dry := fs.Bool("dry-run", false, "muestra el mensaje limpio y los eventos sin hacer commit")
	ws := fs.String("ws", "-", "workstream")
	agent := fs.String("agent", "", "agente que preparó el cambio; queda en el ledger, no en el commit")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fail(2, "coyote commit no recibe rutas: prepáralas con git add o usa -a")
	}
	if len(msgs) == 0 {
		return fail(2, "usa -m \"tipo(ámbito): descripción\"")
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	if !gitx.IsRepo(root) {
		return fail(1, "el proyecto no está en un repo git")
	}
	person := identity.Resolve(root)
	if !person.Configured() {
		return fail(1, "configura git user.name y user.email: los commits llevan la autoría de una persona")
	}
	attr, err := attribution.Default()
	if err != nil {
		return err
	}
	// La autoría efectiva es la que git usaría ahora, incluidas GIT_AUTHOR_* y GIT_COMMITTER_*.
	author := map[string][2]string{}
	for _, who := range []string{"author", "committer"} {
		name, email, err := gitx.Ident(root, who)
		if err != nil {
			return fail(1, "git no tiene identidad de %s: %v", who, err)
		}
		if f, ok := attr.AIIdentity(name, email); ok {
			return fail(1, "R15: el %s del commit sería una herramienta de IA (%s); los commits van con la identidad de una persona", who, f.Text)
		}
		author[who] = [2]string{name, email}
	}
	// Con -m git conserva las líneas con #, así que se revisan todas. Las líneas de
	// atribución se quitan; una frase dentro del texto no se reescribe en silencio.
	clean, found := attr.ScrubLines(msgs.String(), false)
	if attribution.EmptyMessage(clean) {
		return fail(1, "el mensaje quedó vacío al quitar la atribución a herramientas de IA")
	}
	if ph := attr.Phrases(clean, false); len(ph) > 0 {
		return fail(1, "R15: el mensaje dice que lo hizo una herramienta de IA (%s, línea %d: %q); reescríbelo", ph[0].PatternID, ph[0].Line, shortText(ph[0].Text, 80))
	}
	subject := strings.SplitN(strings.TrimSpace(clean), "\n", 2)[0]
	if !*noVerify {
		st, err := standards.Load(root, a.now())
		if err != nil {
			return err
		}
		if r := st.Find("R2"); r != nil && !r.Disabled && r.Level == "MUST" {
			pattern := standards.DefaultCommitPattern
			for _, c := range r.AllChecks() {
				if c.Type == "commit_format" && c.Pattern != "" {
					pattern = c.Pattern
				}
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return err
			}
			if !re.MatchString(subject) {
				return fail(1, "R2: %q no sigue tipo(ámbito): descripción", subject)
			}
		}
		if r := st.Find("R14"); r != nil && !r.Disabled && r.Level == "MUST" {
			for _, name := range []string{ccfdoc.ReadmeFile, ccfdoc.ContextFile} {
				ds := loadDoc(root, name)
				if !ds.present {
					return fail(1, "R14: falta %s; corre coyote init", name)
				}
				if ccfdoc.HasErrors(ds.issues) {
					return fail(1, "R14: %s tiene errores; corre coyote doctor", name)
				}
			}
		}
	}
	typ, scope, desc := parseConventional(subject)
	now := a.now()
	var events []ccf.Line
	if len(found) > 0 {
		what := fmt.Sprintf("%d marcas de atribución a IA quitadas del mensaje", len(found))
		if len(found) == 1 {
			what = "1 marca de atribución a IA quitada del mensaje"
		}
		events = append(events, ccf.Line{TS: now, Actor: person.Actor(*agent), Project: ledgerID(*ws), Repo: cfg.Name, Type: "attr",
			Scope: scope, What: what, Status: "ok"})
	}
	events = append(events, ccf.Line{TS: now, Actor: person.Actor(*agent), Project: ledgerID(*ws), Repo: cfg.Name, Type: typ,
		Scope: scope, What: ccf.ShortWhat(desc, ccf.MaxWhatWords), Status: "ok"})
	if *dry {
		fmt.Fprint(a.stdout, clean)
		for _, e := range events {
			fmt.Fprintln(a.stdout, "evento: "+e.String())
		}
		return nil
	}
	if !gitx.HasStaged(root) && !(*all && gitx.HasTrackedChanges(root)) {
		return fail(1, "no hay cambios para el commit: prepáralos con git add o usa -a")
	}
	led := ledger.Open(root)
	path := led.PathFor(now, person.Slug)
	relPath := rel(root, path)
	backup, backupErr := os.ReadFile(path)
	if backupErr != nil && !os.IsNotExist(backupErr) {
		return backupErr
	}
	staged := gitx.StagedEntry(root, relPath)
	restore := func() {
		if backupErr == nil {
			_ = os.WriteFile(path, backup, 0o644)
		} else {
			_ = os.Remove(path)
		}
		_ = gitx.RestoreStaged(root, relPath, staged)
	}
	for _, e := range events {
		if _, err := led.Append(e, person.Slug); err != nil {
			restore()
			return err
		}
	}
	if err := gitx.Add(root, relPath); err != nil {
		restore()
		return err
	}
	gitArgs := []string{"-C", root, "commit", "-m", clean}
	if *all {
		gitArgs = append(gitArgs, "-a")
	}
	cmd := exec.Command("git", gitArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = a.stdin, a.stdout, a.stderr
	if err := cmd.Run(); err != nil {
		restore()
		return fail(1, "git commit falló; el ledger quedó como estaba")
	}
	fmt.Fprintf(a.stdout, "commit %s · autor %s <%s> · ledger %s\n", gitx.HeadShort(root), author["author"][0], author["author"][1], relPath)
	if len(found) > 0 {
		fmt.Fprintf(a.stderr, "se quitaron %d marcas de atribución a herramientas de IA del mensaje\n", len(found))
	}
	return nil
}

func cmdAttribution(a *app, args []string) error {
	if len(args) == 0 {
		return fail(2, "uso: coyote attribution check [rutas] [--commits N] [--stdin] | scrub [archivo] [--in-place] [--commit-msg]")
	}
	attr, err := attribution.Default()
	if err != nil {
		return err
	}
	switch args[0] {
	case "check":
		return attributionCheck(a, attr, args[1:])
	case "scrub":
		return attributionScrub(a, attr, args[1:])
	}
	return fail(2, "subcomando desconocido %q; usa check o scrub", args[0])
}

func attributionCheck(a *app, attr *attribution.Config, args []string) error {
	fs := a.flags("attribution check", "[rutas] [--commits N] [--stdin]")
	commits := fs.Int("commits", 0, "revisa también los últimos N commits desde que se adoptó coyote")
	stdin := fs.Bool("stdin", false, "revisa el texto de la entrada estándar")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	var found []string
	report := func(where string, fs []attribution.Finding) {
		for _, f := range fs {
			found = append(found, fmt.Sprintf("%s:%d\t%s\t%s", where, f.Line, f.PatternID, shortText(f.Text, 80)))
		}
	}
	if *stdin {
		data, err := io.ReadAll(io.LimitReader(a.stdin, 4<<20))
		if err != nil {
			return err
		}
		report("stdin", attr.Check(string(data), true))
	}
	var root string
	if len(pos) == 0 && !*stdin {
		if root, _, err = a.project(); err != nil {
			return err
		}
		// Mismas rutas y exclusiones que la regla R15 del estándar vigente.
		paths, except := []string{"**/*.md", "**/*.txt"}, []string(nil)
		if st, err := standards.Load(root, a.now()); err == nil {
			if r := st.Find("R15"); r != nil {
				for _, c := range r.AllChecks() {
					if c.Type == "attribution" {
						if len(c.Paths) > 0 {
							paths = c.Paths
						}
						except = append(except, c.Except...)
					}
				}
			}
		}
		files, err := standards.ListFiles(root)
		if err != nil {
			return err
		}
		for _, f := range files {
			if glob.Any(paths, f) && !glob.Any(except, f) && !attr.Allowed(f) {
				if data, err := os.ReadFile(filepath.Join(root, f)); err == nil {
					report(f, attr.Check(string(data), false))
				}
			}
		}
		if *commits == 0 {
			*commits = 50
		}
	}
	for _, p := range pos {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		report(p, attr.Check(string(data), false))
	}
	if *commits > 0 {
		if root == "" {
			if root, _, err = a.project(); err != nil {
				return err
			}
		}
		cms, err := standards.AdoptedCommits(root, *commits, true)
		if err != nil {
			return err
		}
		if gitx.IsShallow(root) {
			found = append(found, "historial\t-\tclon superficial: el historial está incompleto (en CI usa fetch-depth: 0)")
		}
		for _, f := range standards.CommitAttribution(attr, cms) {
			found = append(found, fmt.Sprintf("%s\t-\t%s", f.Path, f.Msg))
		}
	}
	if len(found) == 0 {
		fmt.Fprintln(a.stdout, "sin atribución a herramientas de IA")
		return nil
	}
	tw := table(a.stdout)
	for _, f := range found {
		fmt.Fprintln(tw, f)
	}
	tw.Flush()
	return fail(1, "%d atribuciones a herramientas de IA (R15)", len(found))
}

func attributionScrub(a *app, attr *attribution.Config, args []string) error {
	fs := a.flags("attribution scrub", "[archivo] [--in-place] [--commit-msg]")
	inPlace := fs.Bool("in-place", false, "reescribe el archivo")
	commitMsg := fs.Bool("commit-msg", false, "modo hook commit-msg: respeta comentarios y tijeras de git, detiene el commit ante frases o autoría de herramientas")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	var data []byte
	if len(pos) > 0 {
		data, err = os.ReadFile(pos[0])
	} else {
		data, err = io.ReadAll(io.LimitReader(a.stdin, 4<<20))
	}
	if err != nil {
		return err
	}
	out, found := attr.ScrubLines(string(data), *commitMsg)
	for _, f := range found {
		fmt.Fprintf(a.stderr, "coyote: se quitó atribución a IA (%s): %s\n", f.PatternID, shortText(f.Text, 80))
	}
	phrases := attr.Phrases(out, *commitMsg)
	if *commitMsg {
		// Modo hook: las líneas se quitan; una frase o una identidad de herramienta
		// detienen el commit para que la persona lo corrija.
		if len(phrases) > 0 {
			return fail(1, "coyote: R15: el mensaje dice que lo hizo una herramienta de IA (%s, línea %d: %q); reescríbelo", phrases[0].PatternID, phrases[0].Line, shortText(phrases[0].Text, 80))
		}
		if wd, err := a.workdir(); err == nil && gitx.IsRepo(wd) {
			for _, who := range []string{"author", "committer"} {
				if name, email, err := gitx.Ident(wd, who); err == nil {
					if f, ok := attr.AIIdentity(name, email); ok {
						return fail(1, "coyote: R15: el %s del commit sería una herramienta de IA (%s); usa la identidad de una persona", who, f.Text)
					}
				}
			}
		}
		if attribution.EmptyMessage(out) {
			return fail(1, "coyote: el mensaje quedó vacío al quitar la atribución a herramientas de IA")
		}
	}
	if *inPlace && len(pos) > 0 {
		if len(found) > 0 {
			if err := os.WriteFile(pos[0], []byte(out), 0o644); err != nil {
				return err
			}
		}
	} else if _, err := io.WriteString(a.stdout, out); err != nil {
		return err
	}
	if len(phrases) > 0 {
		// En archivos las frases no se reescriben solas: el sentido de un texto es de la persona.
		for _, p := range phrases {
			fmt.Fprintf(a.stderr, "coyote: reescribe a mano (%s) línea %d: %s\n", p.PatternID, p.Line, shortText(p.Text, 100))
		}
		return fail(1, "")
	}
	return nil
}

var (
	// gitWriteRe reconoce comandos que publican un mensaje: commits, tags, merges,
	// PRs, issues y escrituras con gh api (solo con método o campos de escritura).
	gitWriteRe = regexp.MustCompile(`(?i)\bgit(?:\s+(?:-[Cc]\s+\S+|--(?:git-dir|work-tree|namespace)(?:=|\s+)\S+|-\S+))*\s+(commit|tag|notes\s+(?:add|append|edit|copy)|merge|revert|cherry-pick)\b` +
		`|\bgit\b[^\n;&|]*\bpush\b[^\n;&|]*-o\s*merge_request\.(description|title)` +
		`|\bgh\s+(pr|release|issue)\s+(create|edit|comment|review|merge)\b` +
		`|\bgh\s+api\b[^\n;&|]*(\s-X\s*|\s--method[=\s]\s*)(POST|PATCH|PUT)\b` +
		`|\bgh\s+api\b[^\n;&|]*\s(-f|-F|--field|--raw-field|--input)[\s=]` +
		`|\bglab\s+(mr|release|issue)\s+(create|update|note|merge)\b`)
	// writeToolRe y readToolRe separan herramientas (MCP u otras) que publican
	// texto de las que solo leen: list_commits o search_issues no se revisan.
	writeToolRe = regexp.MustCompile(`(?i)(pull_?request|merge_?request|create_pr|commit|push_files|create_or_update_file|release|comment|review|issue|merge|note)`)
	readToolRe  = regexp.MustCompile(`(?i)(^|[_\-.])(list|get|search|read|fetch|view|download|show)[_\-]`)
	// fileArgRe encuentra mensajes que vienen de un archivo: git commit -F f,
	// -Ff, --file=f, gh pr create --body-file f.
	fileArgRe = regexp.MustCompile(`(?:^|\s)(?:-F|--file|--body-file|--notes-file)(?:=|\s+)?("[^"]+"|'[^']+'|[^\s;&|'"]+)`)
	// cdRe encuentra cambios de directorio previos al comando (cd sub && git commit -F m).
	cdRe = regexp.MustCompile(`(?:^|[;&|(]\s*)cd\s+("[^"]+"|'[^']+'|[^\s;&|)]+)`)
	// identArgRe encuentra identidades puestas en el comando: --author, variables
	// GIT_AUTHOR_* o GIT_COMMITTER_* y -c user.name/user.email.
	identArgRe = regexp.MustCompile(`(?i)--author[=\s]\s*("[^"]*"|'[^']*'|\S+)` +
		`|\bGIT_(?:AUTHOR|COMMITTER)_(NAME|EMAIL)=("[^"]*"|'[^']*'|\S+)` +
		`|-c\s+user\.(name|email)=("[^"]*"|'[^']*'|\S+)`)
)

// hookText extrae de la entrada de un hook el texto que hay que revisar y las
// identidades que el comando fija. Acepta las formas de Claude Code y Codex
// (tool_name, tool_input), Cursor (command), Copilot (toolName, toolArgs como
// objeto o como JSON en texto) y texto plano.
func hookText(data []byte) (string, [][2]string) {
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		// Entrada que no es JSON: se revisa tal cual si parece un commit, tag o PR.
		return shellText(string(data), "")
	}
	cwd, _ := m["cwd"].(string)
	input := firstOf(m, "tool_input", "toolArgs", "toolInput", "input", "arguments")
	if s, ok := input.(string); ok { // toolArgs puede venir como JSON dentro de un texto
		var inner any
		if json.Unmarshal([]byte(s), &inner) == nil {
			input = inner
		}
	}
	var b strings.Builder
	var idents [][2]string
	// Una herramienta que publica texto se revisa completa, traiga o no un "command"
	// (Cursor incluye el comando del servidor MCP en la entrada).
	name, _ := firstOf(m, "tool_name", "toolName", "tool").(string)
	if name != "" && writeToolRe.MatchString(name) && !readToolRe.MatchString(name) {
		collectStrings(input, &b)
	}
	cmd := commandOf(input)
	if cmd == "" {
		cmd = commandOf(m)
	}
	if cmd != "" {
		t, ids := shellText(cmd, cwd)
		b.WriteString(t)
		idents = ids
	}
	return b.String(), idents
}

// shellText devuelve el texto de un comando de shell que publica un mensaje, con
// sus archivos de mensaje, y las identidades que fija; "" si no publica nada.
func shellText(cmd, cwd string) (string, [][2]string) {
	cmd = joinContinuations(cmd)
	if !gitWriteRe.MatchString(cmd) {
		return "", nil
	}
	var idents [][2]string
	var name, email string
	for _, m := range identArgRe.FindAllStringSubmatch(cmd, -1) {
		switch {
		case m[1] != "": // --author "Nombre <correo>"
			n, e := splitAuthor(unquote(m[1]))
			idents = append(idents, [2]string{n, e})
		case m[2] != "":
			if strings.EqualFold(m[2], "name") {
				name = unquote(m[3])
			} else {
				email = unquote(m[3])
			}
		case m[4] != "":
			if strings.EqualFold(m[4], "name") {
				name = unquote(m[5])
			} else {
				email = unquote(m[5])
			}
		}
	}
	if name != "" || email != "" {
		idents = append(idents, [2]string{name, email})
	}
	return cmd + fileContents(cmd, cwd), idents
}

func splitAuthor(s string) (string, string) {
	lt := strings.Index(s, "<")
	if lt < 0 {
		return strings.TrimSpace(s), ""
	}
	return strings.TrimSpace(s[:lt]), strings.Trim(strings.TrimSpace(s[lt:]), "<>")
}

func unquote(s string) string { return strings.Trim(s, `"'`) }

func firstOf(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

// commandOf devuelve el comando de shell de una entrada: texto o lista de argumentos.
func commandOf(v any) string {
	obj, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	switch c := obj["command"].(type) {
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, p := range c {
			if s, ok := p.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// joinContinuations une las líneas terminadas en barra invertida como lo hace
// el shell: la barra y el salto desaparecen, sin agregar espacio.
func joinContinuations(s string) string {
	return strings.NewReplacer("\\\r\n", "", "\\\n", "").Replace(s)
}

// fileContents agrega el contenido de los archivos de mensaje (-F, --body-file),
// buscados en el cwd del hook y en los directorios de los cd del comando.
func fileContents(cmd, cwd string) string {
	bases := []string{cwd}
	for _, m := range cdRe.FindAllStringSubmatch(cmd, 8) {
		d := unquote(m[1])
		if !filepath.IsAbs(d) && cwd != "" {
			d = filepath.Join(cwd, d)
		}
		bases = append(bases, d)
	}
	var b strings.Builder
	for _, m := range fileArgRe.FindAllStringSubmatch(cmd, 8) {
		p := unquote(m[1])
		if p == "" || p == "-" {
			continue // "-" es la entrada estándar: el heredoc ya está en el comando
		}
		for _, base := range bases {
			full := p
			if !filepath.IsAbs(p) && base != "" {
				full = filepath.Join(base, p)
			}
			f, err := os.Open(full)
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(io.LimitReader(f, 1<<20))
			f.Close()
			b.WriteString("\n")
			b.Write(data)
		}
	}
	return b.String()
}

func collectStrings(v any, b *strings.Builder) {
	switch t := v.(type) {
	case string:
		b.WriteString(t)
		b.WriteString("\n")
	case map[string]any:
		for _, x := range t {
			collectStrings(x, b)
		}
	case []any:
		for _, x := range t {
			collectStrings(x, b)
		}
	}
}
