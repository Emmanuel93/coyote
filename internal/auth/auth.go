// Package auth obtiene el token de la persona para la API de GitHub sin
// dejar secretos en disco (ADR-0008). Git no lo necesita: usa el SSH o el
// credential helper de la persona.
//
// Orden de búsqueda: COYOTE_GITHUB_TOKEN, GH_TOKEN, GITHUB_TOKEN, `gh auth
// token` y el llavero del sistema (security en macOS, secret-tool en Linux).
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Service es el nombre con el que coyote guarda tokens en el llavero.
const Service = "coyote"

// ErrNoToken indica que no hay token en ninguna fuente.
var ErrNoToken = errors.New("no hay token de GitHub: usa coyote auth login, gh auth login o COYOTE_GITHUB_TOKEN")

// Runner ejecuta un binario del sistema con entrada estándar; se reemplaza en pruebas.
var Runner = func(name string, args []string, stdin string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// LookPath se reemplaza en pruebas.
var LookPath = exec.LookPath

// GOOS se reemplaza en pruebas.
var GOOS = runtime.GOOS

var (
	tokenRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]{20,255}$`)
	hostRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)
)

func checkHost(host string) error {
	if !hostRe.MatchString(host) {
		return fmt.Errorf("host inválido %q", host)
	}
	return nil
}

// Valid informa si el texto tiene forma de token (sin espacios ni comillas).
func Valid(tok string) bool { return tokenRe.MatchString(tok) }

// Mask muestra solo el prefijo y los últimos cuatro caracteres.
func Mask(tok string) string {
	if len(tok) <= 8 {
		return "****"
	}
	prefix := ""
	if i := strings.Index(tok, "_"); i > 0 && i < 8 {
		prefix = tok[:i+1]
	}
	return prefix + "…" + tok[len(tok)-4:]
}

// Token busca el token para host y dice de qué fuente vino.
func Token(host string) (string, string, error) {
	for _, env := range []string{"COYOTE_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(env)); t != "" {
			if !Valid(t) {
				return "", "", fmt.Errorf("%s no tiene forma de token", env)
			}
			return t, "variable " + env, nil
		}
	}
	if _, err := LookPath("gh"); err == nil {
		if t, err := Runner("gh", []string{"auth", "token", "--hostname", host}, ""); err == nil && Valid(t) {
			return t, "gh auth token", nil
		}
	}
	if t, err := keychainGet(host); err == nil && Valid(t) {
		return t, "llavero del sistema", nil
	}
	return "", "", ErrNoToken
}

// Store guarda el token en el llavero del sistema. Nunca escribe archivos.
func Store(host, tok string) error {
	if err := checkHost(host); err != nil {
		return err
	}
	if !Valid(tok) {
		return errors.New("el texto no tiene forma de token")
	}
	switch GOOS {
	case "darwin":
		// Por la entrada estándar de security -i, así el token no aparece en la lista de procesos.
		_, err := Runner("security", []string{"-i"}, fmt.Sprintf("add-generic-password -U -a %s -s %s -w %s\n", host, Service, tok))
		return err
	case "linux":
		if _, err := LookPath("secret-tool"); err != nil {
			return errors.New("no hay llavero disponible (secret-tool); usa COYOTE_GITHUB_TOKEN o gh auth login")
		}
		_, err := Runner("secret-tool", []string{"store", "--label=coyote " + host, "service", Service, "host", host}, tok)
		return err
	}
	return fmt.Errorf("no sé usar el llavero de %s; usa COYOTE_GITHUB_TOKEN o gh auth login", GOOS)
}

// Delete borra el token del llavero.
func Delete(host string) error {
	if err := checkHost(host); err != nil {
		return err
	}
	switch GOOS {
	case "darwin":
		_, err := Runner("security", []string{"delete-generic-password", "-a", host, "-s", Service}, "")
		return err
	case "linux":
		if _, err := LookPath("secret-tool"); err != nil {
			return nil
		}
		_, err := Runner("secret-tool", []string{"clear", "service", Service, "host", host}, "")
		return err
	}
	return nil
}

func keychainGet(host string) (string, error) {
	if err := checkHost(host); err != nil {
		return "", err
	}
	switch GOOS {
	case "darwin":
		return Runner("security", []string{"find-generic-password", "-a", host, "-s", Service, "-w"}, "")
	case "linux":
		if _, err := LookPath("secret-tool"); err != nil {
			return "", err
		}
		return Runner("secret-tool", []string{"lookup", "service", Service, "host", host}, "")
	}
	return "", ErrNoToken
}

// DeviceCode es la respuesta del primer paso del device flow.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// DeviceFlow implementa el OAuth device flow de GitHub con la OAuth App de la
// organización (client ID público; no hay secreto).
type DeviceFlow struct {
	BaseURL  string // https://github.com
	ClientID string
	Scopes   []string
	HTTP     *http.Client
	Sleep    func(time.Duration)
}

func (d *DeviceFlow) post(ctx context.Context, path string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(d.BaseURL, "/")+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := d.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s respondió %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Start pide el código que la persona escribe en el navegador.
func (d *DeviceFlow) Start(ctx context.Context) (*DeviceCode, error) {
	if d.ClientID == "" {
		return nil, errors.New("falta el client ID de la OAuth App de la organización (se decide en G2)")
	}
	var dc DeviceCode
	err := d.post(ctx, "/login/device/code", url.Values{"client_id": {d.ClientID}, "scope": {strings.Join(d.Scopes, " ")}}, &dc)
	if err == nil && (dc.DeviceCode == "" || dc.UserCode == "") {
		err = errors.New("respuesta de device flow incompleta")
	}
	return &dc, err
}

// Poll espera a que la persona autorice y devuelve el token.
func (d *DeviceFlow) Poll(ctx context.Context, dc *DeviceCode) (string, error) {
	interval := time.Duration(dc.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	sleep := d.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	deadline := time.Duration(dc.ExpiresIn) * time.Second
	if deadline <= 0 {
		deadline = 15 * time.Minute
	}
	for waited := time.Duration(0); waited < deadline; waited += interval {
		sleep(interval)
		var r struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
			Interval    int    `json:"interval"`
		}
		if err := d.post(ctx, "/login/oauth/access_token", url.Values{"client_id": {d.ClientID}, "device_code": {dc.DeviceCode},
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, &r); err != nil {
			return "", err
		}
		switch r.Error {
		case "":
			if !Valid(r.AccessToken) {
				return "", errors.New("GitHub devolvió un token con forma inesperada")
			}
			return r.AccessToken, nil
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
			if r.Interval > 0 {
				interval = time.Duration(r.Interval) * time.Second
			}
		case "expired_token":
			return "", errors.New("el código expiró; vuelve a correr coyote auth login")
		case "access_denied":
			return "", errors.New("autorización cancelada en el navegador")
		default:
			return "", fmt.Errorf("device flow: %s", r.Error)
		}
	}
	return "", errors.New("se acabó el tiempo para autorizar")
}
