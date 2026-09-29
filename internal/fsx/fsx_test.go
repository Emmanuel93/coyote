package fsx

import (
	"os"
	"path/filepath"
	"testing"
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
