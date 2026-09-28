package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccfdoc"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/project"
)

// docsTTL es cuánto se reutiliza la copia de documentos de un repo antes de volver a traerla.
const docsTTL = time.Hour

// docPatterns son las rutas que se traen de otro repo: sus documentos coyote,
// nunca su código (el código vive en su repo y se baja solo si hace falta).
var docPatterns = []string{"/README.coyote.md", "/CONTEXT.coyote.md", "/README.md", "/.coyoteignore", "/coyote/"}

func cmdRepo(a *app, args []string) error {
	if len(args) == 0 {
		return fail(2, "uso: coyote repo add <nombre> [url] [--path P] [--branch B] | list | fetch [nombre]")
	}
	switch args[0] {
	case "add":
		return repoAdd(a, args[1:])
	case "list":
		return repoList(a, args[1:])
	case "fetch":
		return repoFetch(a, args[1:])
	}
	return fail(2, "subcomando desconocido %q; usa add, list o fetch", args[0])
}

func repoAdd(a *app, args []string) error {
	fs := a.flags("repo add", "<nombre> [url] [--path P] [--branch B]")
	path := fs.String("path", "", "copia local del repo, relativa al proyecto o absoluta; coyote solo la lee")
	branch := fs.String("branch", "", "rama de la que se leen los documentos (por defecto, la principal)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 || len(pos) > 2 {
		fs.Usage()
		return fail(2, "")
	}
	r := project.RepoRef{Name: pos[0], Path: *path, Branch: *branch}
	if len(pos) == 2 {
		r.URL = pos[1]
	}
	root, _, err := a.project()
	if err != nil {
		return err
	}
	status, err := project.AddRepo(root, r)
	if err != nil {
		return fail(1, "%v", err)
	}
	fmt.Fprintf(a.stdout, "repo %s %s en %s; su contexto: coyote get context %s\n", r.Name, status, project.ConfigPath, r.Name)
	if cfg, err := project.Load(root); err == nil && cfg.Type == "product" && r.Path != "" {
		fmt.Fprintln(a.stdout, "producto: coyote map arma el mapa de interfaces y coyote extract propone sus documentos")
	}
	return nil
}

func repoList(a *app, args []string) error {
	fs := a.flags("repo list", "")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	if len(cfg.Repos) == 0 {
		fmt.Fprintln(a.stdout, "sin repos registrados; agrega uno con coyote repo add <nombre> <url>")
		return nil
	}
	tw := table(a.stdout)
	fmt.Fprintln(tw, "repo\turl\truta local\tdocumentos")
	for _, r := range cfg.Repos {
		state := "sin traer"
		if t, ok := docsFetchedAt(root, r.Name); ok {
			state = "traídos " + t.UTC().Format("2006-01-02 15:04Z")
		}
		if r.Path != "" {
			state = "copia local"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, orDash(r.URL), orDash(r.Path), state)
	}
	return tw.Flush()
}

func repoFetch(a *app, args []string) error {
	fs := a.flags("repo fetch", "[nombre]")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	n := 0
	for _, r := range cfg.Repos {
		if len(pos) > 0 && r.Name != pos[0] {
			continue
		}
		n++
		dir, err := a.fetchDocs(root, r, true)
		if err != nil {
			fmt.Fprintf(a.stderr, "%s: %v\n", r.Name, err)
			continue
		}
		fmt.Fprintf(a.stdout, "%s: documentos en %s\n", r.Name, rel(root, dir))
	}
	if n == 0 {
		return fail(1, "no hay repos que traer")
	}
	return nil
}

// repoDocs devuelve una carpeta con los documentos coyote del repo: su copia
// local si existe, o una copia parcial (solo documentos) en .coyote/repos/.
func (a *app) repoDocs(root string, r project.RepoRef) (string, error) {
	if r.Path != "" {
		p := r.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		if _, err := os.Stat(filepath.Join(p, ccfdoc.ReadmeFile)); err == nil {
			return p, nil
		}
	}
	// En un producto, la propuesta de coyote extract mientras el repo no tenga
	// sus propios documentos.
	proposed := filepath.Join(root, filepath.FromSlash(reposDir), r.Name)
	if fsx.NoSymlinks(root, reposDir+"/"+r.Name+"/"+ccfdoc.ReadmeFile) == nil && fsx.Regular(root, reposDir+"/"+r.Name+"/"+ccfdoc.ReadmeFile) {
		if r.URL == "" || r.Path != "" {
			return proposed, nil
		}
	}
	if r.URL == "" {
		return "", fmt.Errorf("el repo %s no tiene %s: corre coyote extract %s para proponerlo", r.Name, ccfdoc.ReadmeFile, r.Name)
	}
	return a.fetchDocs(root, r, false)
}

func docsDir(root, name string) (string, string) {
	rel := ".coyote/repos/" + name
	return rel, filepath.Join(root, filepath.FromSlash(rel))
}

func docsFetchedAt(root, name string) (time.Time, bool) {
	_, dir := docsDir(root, name)
	for _, f := range []string{"FETCH_HEAD", "HEAD"} {
		if info, err := os.Stat(filepath.Join(dir, ".git", f)); err == nil {
			return info.ModTime(), true
		}
	}
	return time.Time{}, false
}

// fetchDocs trae con git (tus credenciales) solo los documentos del repo:
// clon superficial, sin blobs y con sparse checkout.
func (a *app) fetchDocs(root string, r project.RepoRef, refresh bool) (string, error) {
	if err := project.ValidateRepo(r); err != nil {
		return "", err
	}
	if r.URL == "" {
		return "", fmt.Errorf("el repo %s no tiene URL ni copia local con %s", r.Name, ccfdoc.ReadmeFile)
	}
	relDir, dir := docsDir(root, r.Name)
	if err := fsx.NoSymlinks(root, relDir); err != nil {
		return "", err
	}
	git := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-c", "protocol.ext.allow=never", "-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(firstLines(string(out), 2)))
		}
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return "", err
		}
		args := []string{"clone", "--quiet", "--depth", "1", "--filter=blob:none", "--no-checkout"}
		if r.Branch != "" {
			args = append(args, "--branch", r.Branch)
		}
		if err := git(append(args, "--", r.URL, dir)...); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
		if err := sparseCheckout(dir, docPatterns, git); err != nil {
			return "", err
		}
	} else if t, ok := docsFetchedAt(root, r.Name); refresh || !ok || time.Since(t) > docsTTL {
		args := []string{"-C", dir, "fetch", "--quiet", "--depth", "1", "--filter=blob:none", "origin"}
		if r.Branch != "" {
			args = append(args, r.Branch)
		}
		if err := git(args...); err != nil {
			fmt.Fprintf(a.stderr, "aviso: %s no se pudo actualizar (%v); uso la copia anterior\n", r.Name, err)
			return dir, nil
		}
		if err := git("-C", dir, "reset", "--quiet", "--hard", "FETCH_HEAD"); err != nil {
			return "", err
		}
	}
	// Los documentos citados en README.coyote.md (docs|ruta|…) también se traen.
	if extra := citedDocs(dir); len(extra) > 0 {
		if err := sparseCheckout(dir, append(append([]string{}, docPatterns...), extra...), git); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func sparseCheckout(dir string, patterns []string, git func(...string) error) error {
	if err := git("-C", dir, "config", "core.sparseCheckout", "true"); err != nil {
		return err
	}
	info := filepath.Join(dir, ".git", "info")
	if err := os.MkdirAll(info, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(info, "sparse-checkout"), []byte(strings.Join(patterns, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return git("-C", dir, "read-tree", "-mu", "HEAD")
}

// citedDocs devuelve patrones para los documentos .md citados en README.coyote.md.
func citedDocs(dir string) []string {
	data, err := fsx.ReadFile(dir, ccfdoc.ReadmeFile, 1<<20)
	if err != nil {
		return nil
	}
	doc, _ := ccfdoc.Parse(ccfdoc.ReadmeFile, data)
	var out []string
	for _, e := range doc.All("docs") {
		if len(e.Fields) == 0 {
			continue
		}
		p := filepath.ToSlash(filepath.Clean(strings.TrimSpace(e.Fields[0])))
		if p == "." || strings.HasPrefix(p, "..") || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "*?[!\\") {
			continue
		}
		if strings.HasSuffix(p, ".md") {
			out = append(out, "/"+p)
		} else {
			out = append(out, "/"+p+"/**/*.md")
		}
	}
	return out
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " ")
}
