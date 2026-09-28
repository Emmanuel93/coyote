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
	commitMsg := fs.Bool("commit-msg", false, "respeta los comentarios de git (líneas con #)")
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
	var out string
	var found []attribution.Finding
	if *commitMsg {
		// En un mensaje de commit se quitan las líneas de atribución; una frase dentro
		// del texto no se reescribe: el commit se detiene para que la persona lo corrija.
		out, found = attr.ScrubLines(string(data), true)
		if ph := attr.Phrases(out, true); len(ph) > 0 {
			return fail(1, "coyote: R15: el mensaje dice que lo hizo una herramienta de IA (%s, línea %d: %q); reescríbelo", ph[0].PatternID, ph[0].Line, shortText(ph[0].Text, 80))
		}
	} else {
		out, found = attr.Scrub(string(data), false)
	}
	for _, f := range found {
		fmt.Fprintf(a.stderr, "coyote: se quitó atribución a IA (%s): %s\n", f.PatternID, shortText(f.Text, 80))
	}
	if *commitMsg && attribution.EmptyMessage(out) {
		return fail(1, "coyote: el mensaje quedó vacío al quitar la atribución a herramientas de IA")
	}
	if *inPlace && len(pos) > 0 {
		if len(found) == 0 {
			return nil
		}
		return os.WriteFile(pos[0], []byte(out), 0o644)
	}
	_, err = io.WriteString(a.stdout, out)
	return err
}

var (
	// gitWriteRe reconoce comandos que publican un mensaje: commits, tags, merges, PRs.
	gitWriteRe = regexp.MustCompile(`(?i)\bgit\b[^\n;&|]*\b(commit|tag|notes|merge|revert|cherry-pick)\b|\bgh\s+(pr|release|issue)\s+(create|edit|comment|review|merge)\b|\bgh\s+api\b|\bglab\s+(mr|release|issue)\s+(create|update|note|merge)\b`)
	// toolNameRe reconoce herramientas (MCP u otras) que escriben commits, PRs, issues o comentarios.
	toolNameRe = regexp.MustCompile(`(?i)(pull_?request|merge_?request|create_pr|commit|push_files|create_or_update_file|release|comment|review|issue|merge)`)
	// fileArgRe encuentra mensajes que vienen de un archivo: git commit -F f, gh pr create --body-file f.
	fileArgRe = regexp.MustCompile(`(?:^|\s)(?:-F|--file|--body-file|--notes-file)(?:\s+|=)("[^"]+"|'[^']+'|[^\s;&|]+)`)
)

func cmdGate(a *app, args []string) error {
	if len(args) == 0 || args[0] != "attribution" {
		return fail(2, "uso: coyote gate attribution < hook.json (PreToolUse de Claude Code, Codex o Copilot; beforeShellExecution de Cursor)")
	}
	data, err := io.ReadAll(io.LimitReader(a.stdin, 1<<20))
	if err != nil {
		return err
	}
	text := hookText(data)
	if text == "" {
		return nil
	}
	attr, err := attribution.Default()
	if err != nil {
		return err
	}
	if found := attr.Check(text, true); len(found) > 0 {
		return fail(2, "coyote: bloqueado por R15: el mensaje lleva atribución a herramientas de IA (%s: %q). Quita esa línea y vuelve a intentar.",
			found[0].PatternID, shortText(found[0].Text, 100))
	}
	return nil
}

// hookText extrae de la entrada de un hook el texto que hay que revisar. Acepta
// las formas de Claude Code y Codex (tool_name, tool_input), Cursor (command),
// Copilot (toolName, toolArgs como objeto o como JSON en texto) y texto plano.
func hookText(data []byte) string {
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		// Entrada que no es JSON: se revisa tal cual si parece un commit, tag o PR.
		if raw := joinContinuations(string(data)); gitWriteRe.MatchString(raw) {
			return raw + fileContents(raw, "")
		}
		return ""
	}
	cwd, _ := m["cwd"].(string)
	input := firstOf(m, "tool_input", "toolArgs", "toolInput", "input", "arguments")
	if s, ok := input.(string); ok { // toolArgs puede venir como JSON dentro de un texto
		var inner any
		if json.Unmarshal([]byte(s), &inner) == nil {
			input = inner
		}
	}
	cmd := commandOf(input)
	if cmd == "" {
		cmd = commandOf(m)
	}
	if cmd != "" {
		cmd = joinContinuations(cmd)
		if gitWriteRe.MatchString(cmd) {
			return cmd + fileContents(cmd, cwd)
		}
		return ""
	}
	name, _ := firstOf(m, "tool_name", "toolName", "tool").(string)
	if name != "" && toolNameRe.MatchString(name) {
		var b strings.Builder
		collectStrings(input, &b)
		return b.String()
	}
	return ""
}

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

// joinContinuations une las líneas terminadas en barra invertida, como hace el shell.
func joinContinuations(s string) string {
	return strings.NewReplacer("\\\r\n", " ", "\\\n", " ").Replace(s)
}

// fileContents agrega el contenido de los archivos de mensaje (-F, --body-file).
func fileContents(cmd, cwd string) string {
	var b strings.Builder
	for _, m := range fileArgRe.FindAllStringSubmatch(cmd, 8) {
		p := strings.Trim(m[1], `"'`)
		if p == "" || p == "-" {
			continue // "-" es la entrada estándar: el heredoc ya está en el comando
		}
		if !filepath.IsAbs(p) && cwd != "" {
			p = filepath.Join(cwd, p)
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(f, 1<<20))
		f.Close()
		b.WriteString("\n")
		b.Write(data)
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
