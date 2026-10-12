package fsx

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestNoSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "coyote", "ledger"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "x.md"), filepath.Join(root, "README.md")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"coyote/ledger/2026/09/28-ana.ccf": true,
		"CONTEXT.coyote.md":                true,
		".claude/settings.json":            false,
		"README.md":                        false,
		"../fuera.md":                      false,
	}
	for rel, ok := range cases {
		if err := NoSymlinks(root, rel); (err == nil) != ok {
			t.Errorf("NoSymlinks(%q) = %v, se esperaba ok=%v", rel, err, ok)
		}
	}
}

func TestWriteAtomicNoSigueUnTemporalPlantado(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victima.txt")
	if err := os.WriteFile(victim, []byte("intacto"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "svc.map")
	// El temporal de nombre fijo de antes, plantado como symlink.
	if err := os.Symlink(victim, target+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(target, []byte("mapa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "intacto" {
		t.Errorf("la escritura salió por el symlink: %q", got)
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		t.Errorf("el destino es un archivo regular: %v %v", info, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "mapa" {
		t.Errorf("contenido: %q", got)
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".svc.map.*.tmp"))
	if len(left) != 0 {
		t.Errorf("quedaron temporales: %v", left)
	}
}

func TestReadCappedNoLeeDispositivos(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "main.tf")
	if err := os.Symlink("/dev/zero", link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCapped(link, 1<<20); err == nil {
		t.Error("un symlink a /dev/zero no se lee")
	}
	big := filepath.Join(dir, "grande.tf")
	if err := os.WriteFile(big, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCapped(big, 1024); err == nil {
		t.Error("un archivo de más del tope no se lee")
	}
	ok := filepath.Join(dir, "ok.tf")
	if err := os.WriteFile(ok, []byte("terraform {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias.tf")
	if err := os.Symlink(ok, alias); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadCapped(alias, 1024); err != nil || string(data) != "terraform {}" {
		t.Errorf("un symlink a un archivo regular se lee: %q %v", data, err)
	}
}

func TestText(t *testing.T) {
	le := []byte{0xFF, 0xFE, 'h', 0, 'o', 0, 'l', 0, 'a', 0}
	be := []byte{0xFE, 0xFF, 0, 'h', 0, 'o', 0, 'l', 0, 'a'}
	bom := append([]byte{0xEF, 0xBB, 0xBF}, "hola"...)
	for name, in := range map[string][]byte{"UTF-16LE": le, "UTF-16BE": be, "UTF-8 con BOM": bom, "UTF-8": []byte("hola")} {
		if s, ok := Text(in); !ok || s != "hola" {
			t.Errorf("%s: %q %v", name, s, ok)
		}
	}
	if _, ok := Text([]byte{'a', 0, 'b'}); ok {
		t.Error("un binario sin BOM no es texto")
	}
}

// TestTextUTF16DeVerdad: un UTF-16 con BOM se lee como texto; bytes cualquiera
// que casualmente empiezan con FF FE no (decodificados serían basura, y una
// basura con controles de dirección o ancho cero se tomaría por un ataque).
func TestTextUTF16DeVerdad(t *testing.T) {
	le := func(s string) []byte {
		out := []byte{0xFF, 0xFE}
		for _, u := range utf16.Encode([]rune(s)) {
			out = append(out, byte(u), byte(u>>8))
		}
		return out
	}
	texto := "línea uno\r\nñandú 😀 日本語\r\n\tfin\r\n"
	if s, ok := Text(le(texto)); !ok || s != texto {
		t.Errorf("UTF-16 de verdad: %q %v", s, ok)
	}
	// Bytes sin forma de texto con el BOM delante.
	rnd := rand.New(rand.NewSource(7))
	for i := 0; i < 20; i++ {
		data := make([]byte, 64<<10)
		rnd.Read(data)
		data[0], data[1] = 0xFF, 0xFE
		if s, ok := Text(data); ok {
			t.Fatalf("bytes al azar con BOM no son texto (%d caracteres)", len([]rune(s)))
		}
	}
	// Un par de caracteres de control en un texto largo no lo vuelven binario.
	largo := strings.Repeat("hola mundo\n", 500) + "\x0c"
	if _, ok := Text(le(largo)); !ok {
		t.Error("un salto de página suelto en un texto largo")
	}
}

// TestTextUTF16ConControlesDeTerminal: sexta revisión de v1.0. Contar los
// caracteres de control entre lo "roto" dejaba por binario un log de PowerShell
// con secuencias ANSI (ESC) y un archivo con unos pocos controles puestos a
// propósito: ninguno de los dos se escaneaba. Lo que delata a un binario son
// los pares de sustitución rotos y los NUL, no los controles.
func TestTextUTF16ConControlesDeTerminal(t *testing.T) {
	le := func(s string) []byte {
		out := []byte{0xFF, 0xFE}
		for _, u := range utf16.Encode([]rune(s)) {
			out = append(out, byte(u), byte(u>>8))
		}
		return out
	}
	ansi := string(rune(0x1b))
	log := ""
	for i := 0; i < 40; i++ {
		log += ansi + "[32mok" + ansi + "[0m paso " + strings.Repeat("x", i%7) + "\r\n"
	}
	log += "GITHUB_TOKEN=ghp_" + strings.Repeat("a1B2", 9) + "\r\n"
	if s, ok := Text(le(log)); !ok || s != log {
		t.Errorf("un log con secuencias ANSI es texto: ok=%v", ok)
	}
	// Doce controles en un archivo corto no lo vuelven binario.
	corto := "/* " + strings.Repeat(string(rune(1)), 12) + " */ x\n"
	if s, ok := Text(le(corto)); !ok || s != corto {
		t.Errorf("controles puestos a propósito: ok=%v", ok)
	}
	// Un sustituto roto suelto en un texto corto, tampoco.
	suelto := []byte{0xFF, 0xFE, 'a', 0, 0x00, 0xD8, 'b', 0}
	if s, ok := Text(suelto); !ok || !strings.HasSuffix(s, "b") {
		t.Errorf("un solo sustituto roto en un texto corto: %q %v", s, ok)
	}
	// Muchos sustitutos rotos, o NUL, sí delatan un binario.
	roto := []byte{0xFF, 0xFE}
	for i := 0; i < 200; i++ {
		roto = append(roto, 'a', 0)
		if i%10 == 0 {
			roto = append(roto, 0x00, 0xD8)
		}
	}
	if _, ok := Text(roto); ok {
		t.Error("10% de sustitutos rotos no es texto")
	}
	nul := []byte{0xFF, 0xFE}
	for i := 0; i < 200; i++ {
		nul = append(nul, 'a', 0)
		if i%20 == 0 {
			nul = append(nul, 0, 0)
		}
	}
	if _, ok := Text(nul); ok {
		t.Error("5% de NUL no es texto")
	}
}

func TestReadFileYRegular(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hola"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadFile(root, "a.txt", 10); err != nil || string(data) != "hola" {
		t.Errorf("ReadFile: %q %v", data, err)
	}
	if _, err := ReadFile(root, "b.txt", 10); err == nil {
		t.Error("ReadFile no sigue symlinks")
	}
	if _, err := ReadFile(root, "a.txt", 2); err == nil {
		t.Error("ReadFile respeta el tope")
	}
	if !Regular(root, "a.txt") || Regular(root, "b.txt") || Regular(root, "no.txt") {
		t.Error("Regular")
	}
}
