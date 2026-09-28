package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const tok = "gho_0123456789abcdefghijklmnop"

type fakeSystem struct {
	calls    []string
	stdin    []string
	keychain map[string]string
	hasGH    bool
}

func (f *fakeSystem) install(t *testing.T, goos string) {
	t.Helper()
	oldRun, oldLook, oldOS := Runner, LookPath, GOOS
	t.Cleanup(func() { Runner, LookPath, GOOS = oldRun, oldLook, oldOS })
	GOOS = goos
	LookPath = func(name string) (string, error) {
		if name == "gh" && !f.hasGH {
			return "", errors.New("no")
		}
		return "/usr/bin/" + name, nil
	}
	Runner = func(name string, args []string, stdin string) (string, error) {
		f.calls = append(f.calls, name+" "+strings.Join(args, " "))
		f.stdin = append(f.stdin, stdin)
		switch {
		case name == "gh":
			return tok, nil
		case name == "security" && len(args) > 0 && args[0] == "-i":
			f.keychain["github.com"] = tok
			return "", nil
		case name == "security" && args[0] == "find-generic-password":
			if v, ok := f.keychain["github.com"]; ok {
				return v, nil
			}
			return "", errors.New("not found")
		case name == "secret-tool" && args[0] == "store":
			f.keychain["github.com"] = stdin
			return "", nil
		case name == "secret-tool" && args[0] == "lookup":
			if v, ok := f.keychain["github.com"]; ok {
				return v, nil
			}
			return "", errors.New("not found")
		}
		return "", nil
	}
	for _, e := range []string{"COYOTE_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(e, "")
	}
}

func TestTokenSources(t *testing.T) {
	f := &fakeSystem{keychain: map[string]string{}}
	f.install(t, "darwin")
	if _, _, err := Token("github.com"); !errors.Is(err, ErrNoToken) {
		t.Fatalf("sin fuentes debe fallar con ErrNoToken: %v", err)
	}
	if err := Store("github.com", tok); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if strings.Contains(c, tok) {
			t.Fatalf("el token no debe ir en los argumentos (visibles en ps): %s", c)
		}
	}
	if got, src, err := Token("github.com"); err != nil || got != tok || src != "llavero del sistema" {
		t.Fatalf("llavero: %q %q %v", got, src, err)
	}
	f.hasGH = true
	if _, src, _ := Token("github.com"); src != "gh auth token" {
		t.Fatalf("gh tiene prioridad sobre el llavero: %q", src)
	}
	t.Setenv("GH_TOKEN", tok)
	if _, src, _ := Token("github.com"); src != "variable GH_TOKEN" {
		t.Fatalf("la variable tiene prioridad: %q", src)
	}
	t.Setenv("GH_TOKEN", "no es un token")
	if _, _, err := Token("github.com"); err == nil {
		t.Fatal("una variable con forma inválida debe fallar")
	}
	if err := Store("github.com; rm -rf x", tok); err == nil {
		t.Fatal("un host con caracteres raros debe rechazarse")
	}
	if Mask(tok) != "gho_…mnop" {
		t.Fatalf("Mask = %q", Mask(tok))
	}
}

func TestLinuxKeychainUsesStdin(t *testing.T) {
	f := &fakeSystem{keychain: map[string]string{}}
	f.install(t, "linux")
	if err := Store("github.com", tok); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(f.calls, " "), tok) || f.keychain["github.com"] != tok {
		t.Fatalf("secret-tool debe recibir el token por la entrada estándar: %v", f.calls)
	}
}

func TestDeviceFlow(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/login/device/code":
			if r.Form.Get("client_id") != "Iv1.abc" {
				t.Errorf("client_id inesperado")
			}
			w.Write([]byte(`{"device_code":"dev","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`))
		case "/login/oauth/access_token":
			polls++
			switch polls {
			case 1:
				w.Write([]byte(`{"error":"authorization_pending"}`))
			case 2:
				w.Write([]byte(`{"error":"slow_down","interval":10}`))
			default:
				w.Write([]byte(`{"access_token":"` + tok + `","token_type":"bearer"}`))
			}
		}
	}))
	defer srv.Close()
	var slept time.Duration
	d := &DeviceFlow{BaseURL: srv.URL, ClientID: "Iv1.abc", Scopes: []string{"repo"}, Sleep: func(x time.Duration) { slept += x }}
	dc, err := d.Start(context.Background())
	if err != nil || dc.UserCode != "ABCD-1234" {
		t.Fatalf("Start: %+v %v", dc, err)
	}
	got, err := d.Poll(context.Background(), dc)
	if err != nil || got != tok || polls != 3 || slept != 20*time.Second {
		t.Fatalf("Poll: %q %v polls=%d slept=%v", got, err, polls, slept)
	}
	if _, err := (&DeviceFlow{BaseURL: srv.URL}).Start(context.Background()); err == nil {
		t.Fatal("sin client ID debe fallar")
	}
}
