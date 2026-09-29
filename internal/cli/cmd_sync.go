package cli

import (
	"bufio"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/attribution"
	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/gitx"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/standards"
)

var scpHostRe = regexp.MustCompile(`^[^@/]+@([^:/]+):`)

// remoteHost devuelve el host de un remoto para el ritmo por cuenta.
func remoteHost(u string) string {
	if m := scpHostRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Hostname()
	}
	return "local"
}

// protectedBranch son las ramas que publica una persona, nunca un agente (A4,
// R17). Sin distinguir mayúsculas: en sistemas de archivos que no las
// distinguen, Main y main son la misma rama.
func protectedBranch(b string) bool {
	b = strings.ToLower(strings.TrimPrefix(b, "refs/heads/"))
	return b == "main" || b == "master" || b == "trunk" || strings.HasPrefix(b, "release/")
}

// maxOutgoing es el máximo de commits que push revisa; más que eso se publica por partes.
const maxOutgoing = 5000

func cmdPush(a *app, args []string) error {
	fs := a.flags("push", "[--remote origin] [--agent A] [--dry-run]")
	remote := fs.String("remote", "origin", "remoto de git")
	agent := fs.String("agent", "", "agente que publica a nombre de la persona")
	dry := fs.Bool("dry-run", false, "revisa y muestra qué se publicaría, sin publicar")
	noVerify := fs.Bool("no-verify", false, "omite el lint (solo una persona; queda en el ledger)")
	profile := fs.String("pace", "", "perfil de ritmo: human o batch (por defecto, el de project.yaml)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fail(2, "coyote push publica la rama actual; no recibe argumentos (usa --remote)")
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	if !gitx.IsRepo(root) || !gitx.HasCommits(root) {
		return fail(1, "no hay commits que publicar")
	}
	branch := gitx.Branch(root)
	if branch == "(detached)" {
		return fail(1, "HEAD está desacoplado; cambia a una rama antes de publicar")
	}
	remoteURL, err := gitx.Run(root, "remote", "get-url", "--", *remote)
	if err != nil {
		return fail(1, "no existe el remoto %q: agrégalo con git remote add %s <url>", *remote, *remote)
	}
	if *agent, err = a.agentFor(root, *agent); err != nil {
		return err
	}
	if (*agent != "" || cfg.Autonomy == "autonomous") && protectedBranch(branch) {
		return fail(1, "A4/R17: un agente publica en ramas de trabajo (ws/…, coyote/…); %s la publica una persona", branch)
	}
	if *noVerify && *agent != "" {
		return fail(1, "--no-verify es solo para personas")
	}
	attr, err := attribution.Default()
	if err != nil {
		return err
	}
	for _, who := range []string{"author", "committer"} {
		if name, email, err := gitx.Ident(root, who); err == nil {
			if f, ok := attr.AIIdentity(name, email); ok {
				return fail(1, "R15: el %s configurado es una herramienta de IA (%s)", who, f.Text)
			}
		}
	}
	_, upErr := gitx.Run(root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	// Lo que sale es todo lo que el remoto todavía no tiene, sea cual sea el
	// upstream configurado: un upstream apuntando a una rama local no esconde commits.
	revs := []string{"HEAD", "--not", "--remotes=" + *remote}
	countOut, err := gitx.Run(root, append([]string{"rev-list", "--count"}, revs...)...)
	if err != nil {
		return err
	}
	count, err := strconv.Atoi(strings.TrimSpace(countOut))
	if err != nil {
		return err
	}
	if count > maxOutgoing {
		return fail(1, "hay %d commits por publicar; coyote revisa hasta %d: publica por partes", count, maxOutgoing)
	}
	outgoing, err := gitx.Log(root, count+1, true, revs...)
	if err != nil {
		return err
	}
	if len(outgoing) == 0 {
		fmt.Fprintf(a.stdout, "nada que publicar: %s ya está en %s\n", branch, *remote)
		return nil
	}
	// Lo que sale no lleva atribución de IA ni autoría de herramientas, ni en merges.
	if found := standards.CommitAttribution(attr, outgoing); len(found) > 0 {
		for _, f := range found {
			fmt.Fprintf(a.stderr, "  %s\t%s\n", f.Path, f.Msg)
		}
		return fail(1, "R15: %d commits por publicar llevan atribución a herramientas de IA; corrígelos antes de publicar", len(found))
	}
	st, res, err := a.lint(root, cfg, false)
	if err != nil {
		return err
	}
	if r := st.Find("R2"); r != nil && !r.Disabled && r.Level == "MUST" && !*noVerify {
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
		for _, c := range outgoing {
			if !c.Merge && !re.MatchString(c.Subject) {
				return fail(1, "R2: el commit %s %q no sigue tipo(ámbito): descripción", c.Short(), shortText(c.Subject, 60))
			}
		}
	}
	if n := res.Failing(false); n > 0 && !*noVerify {
		return fail(1, "el estándar tiene %d hallazgos MUST; corre coyote standards lint antes de publicar", n)
	}
	lim, err := a.limiter(*profile, remoteHost(remoteURL))
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%d commits de %s a %s/%s · autoría revisada · estándar en verde\n", len(outgoing), branch, *remote, branch)
	if *dry {
		fmt.Fprintln(a.stdout, "(dry-run: no se publicó nada)")
		return nil
	}
	waited, err := lim.Wait("write")
	if err != nil {
		return fail(1, "%v", err)
	}
	if waited > 0 {
		fmt.Fprintf(a.stderr, "ritmo humano: se esperó %s antes de publicar\n", waited.Round(time.Second))
	}
	gitArgs := []string{"-C", root, "push"}
	if upErr != nil {
		gitArgs = append(gitArgs, "--set-upstream")
	}
	gitArgs = append(gitArgs, "--", *remote, "HEAD:refs/heads/"+branch)
	cmd := exec.Command("git", gitArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = a.stdin, a.rawOut, a.rawErr
	if err := cmd.Run(); err != nil {
		return fail(1, "git push falló; nada cambió en el ledger")
	}
	status := "ok"
	what := fmt.Sprintf("push de %d commits a %s/%s", len(outgoing), *remote, branch)
	if *noVerify {
		status, what = "skip", what+" sin lint"
	}
	// El push ya ocurrió: si el ledger falla, se avisa, pero el resultado es éxito.
	if err := a.recordSync(root, cfg.Name, *agent, ccf.ShortWhat(what, ccf.MaxWhatWords), []string{"sha:" + gitx.HeadShort(root)}, status); err != nil {
		fmt.Fprintf(a.stderr, "aviso: publicado, pero el evento no quedó en el ledger: %v\n", err)
	}
	return nil
}

func (a *app) recordSync(root, repo, agent, what string, refs []string, status string) error {
	person := identity.Resolve(root)
	line := ccf.Line{TS: a.now(), Actor: person.Actor(agent), Project: "-", Repo: repo, Type: "sync", Scope: "-",
		What: what, Refs: refs, Status: status}
	p, err := ledger.Open(root).Append(line, person.Slug)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stderr, "registrado en %s (entra en tu próximo coyote commit)\n", rel(root, p))
	return nil
}

func cmdPull(a *app, args []string) error {
	fs := a.flags("pull", "[--remote origin]")
	remote := fs.String("remote", "origin", "remoto de git")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fail(2, "coyote pull actualiza la rama actual; no recibe argumentos (usa --remote)")
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	remoteURL, err := gitx.Run(root, "remote", "get-url", "--", *remote)
	if err != nil {
		return fail(1, "no existe el remoto %q", *remote)
	}
	lim, err := a.limiter("", remoteHost(remoteURL))
	if err != nil {
		return err
	}
	if _, err := lim.Wait("read"); err != nil {
		return fail(1, "%v", err)
	}
	before := ""
	if gitx.HasCommits(root) {
		before, _ = gitx.Run(root, "rev-parse", "HEAD")
	}
	cmd := exec.Command("git", "-C", root, "pull", "--ff-only", "--", *remote, gitx.Branch(root))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = a.stdin, a.rawOut, a.rawErr
	if err := cmd.Run(); err != nil {
		return fail(1, "git pull --ff-only falló: si divergiste, integra con git y vuelve a correr coyote pull")
	}
	after, _ := gitx.Run(root, "rev-parse", "HEAD")
	if before == after {
		fmt.Fprintln(a.stdout, "ya estabas al día")
		return nil
	}
	rng := after
	if before != "" {
		rng = before + ".." + after
	}
	count, _ := gitx.Run(root, "rev-list", "--count", rng)
	authors := map[string]bool{}
	if out, err := gitx.Run(root, "log", "--format=%an", rng); err == nil {
		for _, a := range strings.Split(out, "\n") {
			if a = strings.TrimSpace(a); a != "" {
				authors[a] = true
			}
		}
	}
	names := make([]string, 0, len(authors))
	for n := range authors {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(a.stdout, "%s commits nuevos de %s\n", strings.TrimSpace(count), strings.Join(names, ", "))
	diffArgs := []string{"diff", "--unified=0", after, "--", "coyote/ledger"}
	if before != "" {
		diffArgs = []string{"diff", "--unified=0", before, after, "--", "coyote/ledger"}
	}
	events := 0
	if out, err := gitx.Run(root, diffArgs...); err == nil {
		sc := bufio.NewScanner(strings.NewReader(out))
		for sc.Scan() {
			l := sc.Text()
			if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
				if _, err := ccf.Parse(strings.TrimPrefix(l, "+")); err == nil {
					events++
				}
			}
		}
	}
	changed := []string{}
	if before != "" {
		if out, err := gitx.Run(root, "diff", "--name-only", before, after, "--", "README.coyote.md", "CONTEXT.coyote.md", "coyote/decisions", "coyote/standards"); err == nil && out != "" {
			changed = strings.Split(out, "\n")
		}
	}
	fmt.Fprintf(a.stdout, "%d eventos nuevos en el ledger", events)
	if len(changed) > 0 {
		fmt.Fprintf(a.stdout, " · cambió el contexto: %s", strings.Join(changed, ", "))
	}
	fmt.Fprintln(a.stdout)
	if status, err := a.refreshAgents(root, cfg); err == nil && status == "actualizado" {
		fmt.Fprintln(a.stdout, "AGENTS.md actualizado con el contexto nuevo")
	}
	return a.recordSync(root, cfg.Name, "", ccf.ShortWhat(fmt.Sprintf("pull de %s commits desde %s", strings.TrimSpace(count), *remote), ccf.MaxWhatWords),
		[]string{"sha:" + gitx.HeadShort(root)}, "ok")
}
