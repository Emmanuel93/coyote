// Package gitx envuelve el git del sistema. Coyote escribe con el git del
// usuario para respetar su identidad, su firma y sus credenciales (ADR-0002).
package gitx

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Commit es un commit leído del historial.
type Commit struct {
	SHA, AuthorName, AuthorEmail, CommitterName, CommitterEmail, Subject, Body string
	Merge                                                                      bool
}

// Short devuelve los primeros siete caracteres del SHA.
func (c Commit) Short() string {
	if len(c.SHA) > 7 {
		return c.SHA[:7]
	}
	return c.SHA
}

// Message devuelve asunto y cuerpo.
func (c Commit) Message() string {
	if strings.TrimSpace(c.Body) == "" {
		return c.Subject
	}
	return c.Subject + "\n\n" + c.Body
}

// Run ejecuta git en dir y devuelve la salida estándar.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(out.String()), fmt.Errorf("git %s: %s", strings.Join(args, " "), firstLine(errb.String(), err))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

func firstLine(s string, err error) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return err.Error()
	}
	return strings.SplitN(s, "\n", 2)[0]
}

// Version devuelve la versión de git instalada o "" si no hay git.
func Version() string {
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "git version"))
}

// IsRepo informa si dir está dentro de un repo git.
func IsRepo(dir string) bool {
	_, err := Run(dir, "rev-parse", "--git-dir")
	return err == nil
}

// TopLevel devuelve la raíz del repo que contiene dir.
func TopLevel(dir string) (string, error) {
	return Run(dir, "rev-parse", "--show-toplevel")
}

// Init crea un repo con rama main.
func Init(dir string) error {
	if _, err := Run(dir, "init", "-q", "-b", "main"); err == nil {
		return nil
	}
	if _, err := Run(dir, "init", "-q"); err != nil {
		return err
	}
	_, err := Run(dir, "symbolic-ref", "HEAD", "refs/heads/main")
	return err
}

// Config lee una clave de configuración; "" si no existe.
func Config(dir, key string) string {
	out, err := Run(dir, "config", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// HasCommits informa si HEAD apunta a un commit.
func HasCommits(dir string) bool {
	_, err := Run(dir, "rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

// Branch devuelve la rama actual, también en un repo sin commits.
func Branch(dir string) string {
	out, err := Run(dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return "(detached)"
	}
	return out
}

// HeadShort devuelve el SHA corto de HEAD o "".
func HeadShort(dir string) string {
	out, err := Run(dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// Dirty devuelve cuántas rutas tienen cambios sin commit.
func Dirty(dir string) int {
	out, err := Run(dir, "status", "--porcelain")
	if err != nil || strings.TrimSpace(out) == "" {
		return 0
	}
	return len(strings.Split(out, "\n"))
}

const (
	recSep   = "\x1e"
	fieldSep = "\x1f"
)

// Log devuelve hasta n commits de las revisiones dadas, con el mensaje
// completo (%B). Con merges=false omite los merges.
func Log(dir string, n int, merges bool, revs ...string) ([]Commit, error) {
	if !HasCommits(dir) {
		return nil, nil
	}
	args := []string{"log", "-n", strconv.Itoa(n),
		"--format=%H" + fieldSep + "%P" + fieldSep + "%an" + fieldSep + "%ae" + fieldSep + "%cn" + fieldSep + "%ce" + fieldSep + "%B" + recSep}
	if !merges {
		args = append(args, "--no-merges")
	}
	args = append(args, revs...)
	out, err := Run(dir, args...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, rec := range strings.Split(out, recSep) {
		rec = strings.Trim(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, fieldSep, 7)
		if len(f) < 7 {
			continue
		}
		msg := strings.TrimSpace(f[6])
		subject, body, _ := strings.Cut(msg, "\n")
		commits = append(commits, Commit{SHA: f[0], Merge: len(strings.Fields(f[1])) > 1,
			AuthorName: f[2], AuthorEmail: f[3], CommitterName: f[4], CommitterEmail: f[5],
			Subject: strings.TrimSpace(subject), Body: strings.TrimSpace(body)})
	}
	return commits, nil
}

// IsShallow informa si el repo es un clon superficial (historial incompleto).
func IsShallow(dir string) bool {
	out, err := Run(dir, "rev-parse", "--is-shallow-repository")
	return err == nil && strings.TrimSpace(out) == "true"
}

// Ident devuelve nombre y correo efectivos de "author" o "committer": los que
// git usaría ahora, incluidas las variables GIT_AUTHOR_* y GIT_COMMITTER_*.
func Ident(dir, who string) (name, email string, err error) {
	v := "GIT_AUTHOR_IDENT"
	if who == "committer" {
		v = "GIT_COMMITTER_IDENT"
	}
	out, err := Run(dir, "var", v)
	if err != nil {
		return "", "", err
	}
	lt, gt := strings.Index(out, "<"), strings.LastIndex(out, ">")
	if lt < 0 || gt < lt {
		return strings.TrimSpace(out), "", nil
	}
	return strings.TrimSpace(out[:lt]), strings.TrimSpace(out[lt+1 : gt]), nil
}

// HasStaged informa si el índice tiene cambios respecto de HEAD (o del árbol vacío).
func HasStaged(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "diff", "--cached", "--quiet")
	return cmd.Run() != nil
}

// HasTrackedChanges informa si hay archivos versionados modificados sin preparar.
func HasTrackedChanges(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "diff", "--quiet")
	return cmd.Run() != nil
}

// StagedEntry devuelve "modo sha" de path en el índice, o "" si no está.
func StagedEntry(dir, path string) string {
	out, err := Run(dir, "ls-files", "-s", "--", path)
	if err != nil || out == "" {
		return ""
	}
	f := strings.Fields(out)
	if len(f) < 2 {
		return ""
	}
	return f[0] + " " + f[1]
}

// RestoreStaged deja path en el índice como estaba (entry de StagedEntry) o lo quita.
func RestoreStaged(dir, path, entry string) error {
	if entry == "" {
		return Unstage(dir, path)
	}
	f := strings.Fields(entry)
	_, err := Run(dir, "update-index", "--cacheinfo", f[0]+","+f[1]+","+path)
	return err
}

// ListFiles lista archivos versionados y no ignorados, relativos a dir.
func ListFiles(dir string) ([]string, error) {
	out, err := Run(dir, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	seen := map[string]bool{}
	for _, f := range strings.Split(out, "\x00") {
		if f != "" && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	return files, nil
}

// HooksDir devuelve el directorio de hooks del repo (respeta core.hooksPath) e
// informa si queda fuera del repo, como pasa con un core.hooksPath global.
func HooksDir(dir string) (string, bool, error) {
	out, err := Run(dir, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", false, err
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(dir, out)
	}
	out = filepath.Clean(out)
	inside := false
	for _, base := range []string{"--git-common-dir", "--show-toplevel"} {
		b, err := Run(dir, "rev-parse", base)
		if err != nil || b == "" {
			continue
		}
		if !filepath.IsAbs(b) {
			b = filepath.Join(dir, b)
		}
		if r, err := filepath.Rel(filepath.Clean(b), out); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			inside = true
		}
	}
	return out, !inside, nil
}

// Add agrega rutas al índice.
func Add(dir string, paths ...string) error {
	_, err := Run(dir, append([]string{"add", "--"}, paths...)...)
	return err
}

// Unstage quita rutas del índice sin tocar el árbol de trabajo.
func Unstage(dir string, paths ...string) error {
	if !HasCommits(dir) {
		_, err := Run(dir, append([]string{"rm", "--cached", "-q", "--"}, paths...)...)
		return err
	}
	_, err := Run(dir, append([]string{"reset", "-q", "--"}, paths...)...)
	return err
}
