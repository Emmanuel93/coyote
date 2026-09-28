package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/auth"
	"github.com/Emmanuel93/coyote/internal/github"
	"github.com/Emmanuel93/coyote/internal/pace"
	"github.com/Emmanuel93/coyote/internal/version"
)

// paceSleep es la espera real del ritmo humano; las pruebas la reemplazan.
var paceSleep = time.Sleep

func apiBase(host string) string {
	if v := os.Getenv("COYOTE_GITHUB_API"); v != "" {
		return v
	}
	if host == "github.com" {
		return github.DefaultAPI
	}
	return "https://" + host + "/api/v3"
}

func webBase(host string) string {
	if v := os.Getenv("COYOTE_GITHUB_WEB"); v != "" {
		return v
	}
	return "https://" + host
}

func (a *app) limiter(profile, host string) (*pace.Limiter, error) {
	if profile == "" {
		profile = "human"
		if _, cfg, err := a.project(); err == nil && cfg.Pace.Profile != "" {
			profile = cfg.Pace.Profile
		}
	}
	l, err := pace.Open(profile, host)
	if err != nil {
		return nil, err
	}
	l.Sleep = paceSleep
	return l, nil
}

func cmdAuth(a *app, args []string) error {
	if len(args) == 0 {
		return fail(2, "uso: coyote auth login [--with-token | --device --client-id ID] | status [--check] | logout")
	}
	fs := a.flags("auth "+args[0], "[--host github.com]")
	host := fs.String("host", "github.com", "host de GitHub")
	withToken := fs.Bool("with-token", false, "lee el token de la entrada estándar y lo guarda en el llavero")
	device := fs.Bool("device", false, "OAuth device flow con la OAuth App de la organización")
	clientID := fs.String("client-id", os.Getenv("COYOTE_OAUTH_CLIENT_ID"), "client ID de la OAuth App (device flow)")
	check := fs.Bool("check", false, "verifica el token contra la API (una lectura)")
	if _, err := parseArgs(fs, args[1:]); err != nil {
		return err
	}
	switch args[0] {
	case "login":
		return authLogin(a, *host, *withToken, *device, *clientID)
	case "status":
		return authStatus(a, *host, *check)
	case "logout":
		if err := auth.Delete(*host); err != nil {
			return fail(1, "no se pudo borrar del llavero: %v", err)
		}
		fmt.Fprintf(a.stdout, "token de %s borrado del llavero; gh y las variables de entorno no se tocan\n", *host)
		return nil
	}
	return fail(2, "subcomando desconocido %q; usa login, status o logout", args[0])
}

func authLogin(a *app, host string, withToken, device bool, clientID string) error {
	var tok string
	switch {
	case withToken:
		line, err := bufio.NewReader(a.stdin).ReadString('\n')
		if err != nil && line == "" {
			return fail(1, "no llegó ningún token por la entrada estándar")
		}
		tok = strings.TrimSpace(line)
	case device:
		d := &auth.DeviceFlow{BaseURL: webBase(host), ClientID: clientID, Scopes: []string{"repo", "read:org"}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		dc, err := d.Start(ctx)
		if err != nil {
			return fail(1, "%v", err)
		}
		fmt.Fprintf(a.stderr, "Abre %s y escribe el código %s\n", dc.VerificationURI, dc.UserCode)
		if tok, err = d.Poll(ctx, dc); err != nil {
			return fail(1, "%v", err)
		}
	default:
		if t, src, err := auth.Token(host); err == nil {
			fmt.Fprintf(a.stdout, "ya hay token de %s (%s, %s); coyote lo usa tal cual\n", host, src, auth.Mask(t))
			return nil
		}
		return fail(1, "sin token: usa gh auth login, coyote auth login --with-token < archivo, o --device con la OAuth App de tu organización")
	}
	if !auth.Valid(tok) {
		return fail(1, "el texto no tiene forma de token")
	}
	if err := auth.Store(host, tok); err != nil {
		return fail(1, "%v", err)
	}
	fmt.Fprintf(a.stdout, "token de %s guardado en el llavero del sistema (%s); nunca se escribe en el proyecto\n", host, auth.Mask(tok))
	return nil
}

func authStatus(a *app, host string, check bool) error {
	tok, src, err := auth.Token(host)
	if err != nil {
		fmt.Fprintln(a.stdout, "sin token de "+host+": "+err.Error())
		return fail(1, "")
	}
	fmt.Fprintf(a.stdout, "token de %s: %s (%s)\n", host, auth.Mask(tok), src)
	lim, err := a.limiter("", host)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, "ritmo: "+lim.Status())
	if !check {
		return nil
	}
	c := &github.Client{Base: apiBase(host), Token: tok, Pace: lim, UserAgent: "coyote/" + version.Version}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	login, scopes, err := c.User(ctx)
	if err != nil {
		return fail(1, "la API rechazó el token: %v", err)
	}
	fmt.Fprintf(a.stdout, "cuenta: @%s · scopes: %s\n", login, strings.Join(scopes, ", "))
	fmt.Fprintln(a.stdout, "ritmo: "+lim.Status())
	return nil
}
