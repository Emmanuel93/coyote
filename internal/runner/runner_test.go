package runner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fake escribe un claude simulado que guarda sus argumentos y su entrada.
func fake(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + dir + "/args\"\ncat > \"" + dir + "/stdin\"\n" + body
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

const okJSON = `{"type":"result","subtype":"success","is_error":false,"result":"# Diseño\nlisto","session_id":"abc-123","num_turns":4,"duration_ms":5000,"total_cost_usd":0.0421,"usage":{"input_tokens":1200,"cache_creation_input_tokens":300,"cache_read_input_tokens":8000,"output_tokens":900},"modelUsage":{"claude-sonnet-x":{"inputTokens":1200,"outputTokens":900,"cacheReadInputTokens":8000,"cacheCreationInputTokens":300,"costUSD":0.04},"claude-haiku-x":{"inputTokens":10,"outputTokens":5,"costUSD":0.0021}},"permission_denials":[{"tool_name":"Write"}]}`

func TestRunOK(t *testing.T) {
	bin, dir := fake(t, "printf '%s' '"+okJSON+"'\n")
	res, err := Run(context.Background(), Request{Bin: bin, Dir: t.TempDir(), Agent: "coyote-architect", Model: "sonnet",
		MaxTurns: 8, MaxUSD: 1.5, AddDirs: []string{"/repos/a", "/repos/b"}, Tools: []string{"Read", "Grep", "Bash"}, Prompt: "tarea: diseña"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() || res.Text != "# Diseño\nlisto" || res.Turns != 4 || res.CostUSD != 0.0421 || res.SessionID != "abc-123" {
		t.Errorf("resultado: %+v", res)
	}
	if res.Usage.InputTotal() != 9500 || res.Usage.CacheRead != 8000 || res.Usage.Output != 900 {
		t.Errorf("tokens: %+v", res.Usage)
	}
	if res.MainModel() != "claude-sonnet-x" || len(res.Models) != 2 || res.Denials != 1 {
		t.Errorf("modelos: %v %d", res.Models, res.Denials)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	got := strings.Join(strings.Fields(string(args)), " ")
	want := "-p --output-format json --agent coyote-architect --permission-mode dontAsk --model sonnet --max-turns 8 --max-budget-usd 1.50 --allowedTools Read,Grep,Bash --add-dir /repos/a --add-dir /repos/b"
	if got != want {
		t.Errorf("argumentos:\n%s\nse esperaba:\n%s", got, want)
	}
	if in, _ := os.ReadFile(filepath.Join(dir, "stdin")); string(in) != "tarea: diseña" {
		t.Errorf("la tarea va por la entrada estándar: %q", in)
	}
}

func TestRunErrores(t *testing.T) {
	// Tope de turnos: código distinto de cero, pero con resultado.
	bin, _ := fake(t, `printf '%s' '{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":8,"total_cost_usd":0.2}'; exit 1`+"\n")
	res, err := Run(context.Background(), Request{Bin: bin, Agent: "a", MaxTurns: 8, MaxUSD: 1})
	if err != nil || res.OK() || res.ExitCode != 1 || res.Status() != "llegó al tope de turnos" || res.CostUSD != 0.2 {
		t.Errorf("tope de turnos: %+v %v", res, err)
	}
	// Salida que no es JSON: error con lo que dijo Claude Code.
	bin, _ = fake(t, "echo 'Invalid API key' >&2; exit 1\n")
	if _, err := Run(context.Background(), Request{Bin: bin, Agent: "a", MaxTurns: 1, MaxUSD: 1}); err == nil || !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("sin JSON se reporta el error: %v", err)
	}
	// Tiempo vencido: se corta y se informa.
	bin, _ = fake(t, "sleep 5\n")
	start := time.Now()
	res, _ = Run(context.Background(), Request{Bin: bin, Agent: "a", MaxTurns: 1, MaxUSD: 1, Timeout: 200 * time.Millisecond})
	if res == nil || !res.TimedOut || time.Since(start) > 4*time.Second {
		t.Errorf("tiempo vencido: %+v en %s", res, time.Since(start))
	}
	// Sin Claude Code instalado.
	if _, err := Run(context.Background(), Request{Bin: filepath.Join(t.TempDir(), "no-existe"), Agent: "a"}); err != ErrNotFound {
		t.Errorf("sin binario: %v", err)
	}
}

func TestParseStream(t *testing.T) {
	out := "{\"type\":\"system\"}\n" + okJSON + "\n"
	res, err := Parse([]byte(out))
	if err != nil || res.SessionID != "abc-123" {
		t.Errorf("última línea result: %+v %v", res, err)
	}
}

func TestParseLista(t *testing.T) {
	res, err := Parse([]byte("[{\"type\":\"system\"}," + okJSON + "]"))
	if err != nil || res.SessionID != "abc-123" || res.CostUSD != 0.0421 {
		t.Errorf("lista de mensajes: %+v %v", res, err)
	}
	if _, err := Parse([]byte(`[{"type":"system"}]`)); err == nil {
		t.Error("una lista sin resultado es un error")
	}
}

func TestArgsResume(t *testing.T) {
	r := Request{Agent: "coyote-dev", MaxTurns: 5, MaxUSD: 1, Resume: "8f1c2a4e-1b2c-4d5e-9f00-aa11bb22cc33"}
	got := strings.Join(Args(r), " ")
	if !strings.Contains(got, "--permission-mode dontAsk --resume 8f1c2a4e-1b2c-4d5e-9f00-aa11bb22cc33 --max-turns 5") {
		t.Errorf("--resume: %s", got)
	}
	// Un id que parece una opción, o raro, no se pasa.
	for _, bad := range []string{"--dangerously-skip-permissions", "-x12345678", "abc", "a b c d e f g h", "sesión-ñ-123456"} {
		r.Resume = bad
		if strings.Contains(strings.Join(Args(r), " "), "--resume") {
			t.Errorf("%q no debe llegar a --resume", bad)
		}
	}
}

func TestRunInterrumpida(t *testing.T) {
	bin, dir := fake(t, "sleep 30 & echo $! > \""+"$(dirname \"$0\")"+"/sleep.pid\"\ntouch \"$(dirname \"$0\")/started\"\nwait\n")
	type out struct {
		res *Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := Run(context.Background(), Request{Bin: bin, Agent: "a", MaxTurns: 1, MaxUSD: 1, Timeout: time.Minute})
		done <- out{res, err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("el claude simulado no arrancó")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Ctrl-C a coyote: la corrida se detiene con todo su grupo y vuelve como interrumpida.
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	var o out
	select {
	case o = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Run no volvió después de Ctrl-C")
	}
	if o.res == nil || !o.res.Interrupted || o.res.OK() || o.res.Status() != "se interrumpió" || o.err == nil {
		t.Fatalf("resultado: %+v %v", o.res, o.err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "sleep.pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	time.Sleep(300 * time.Millisecond)
	if pid > 0 && running(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("el proceso hijo de Claude Code (%d) siguió vivo", pid)
	}
}

// running dice si un proceso sigue vivo; un zombi ya terminó. Sin /proc
// (macOS) solo se sabe si existe.
func running(pid int) bool {
	if data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status"); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(l, "State:"); ok {
				return !strings.HasPrefix(strings.TrimSpace(v), "Z")
			}
		}
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
