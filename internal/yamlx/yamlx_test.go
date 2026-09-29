package yamlx

import (
	"strings"
	"testing"
)

func TestFormaEstricta(t *testing.T) {
	type conf struct {
		A []string `yaml:"a"`
		B int      `yaml:"b"`
	}
	var c conf
	if err := Strict([]byte("a: [x, y]\nb: 2\n"), &c); err != nil || len(c.A) != 2 || c.B != 2 {
		t.Fatalf("válido: %v %+v", err, c)
	}
	if err := Strict([]byte(""), &c); err != nil {
		t.Fatalf("vacío: %v", err)
	}
	bomb := "a: [&s \"" + strings.Repeat("A", 100) + "\"" + strings.Repeat(", *s", 1000) + "]\n"
	for name, bad := range map[string]string{
		"alias":          bomb,
		"ancla":          "a: &x [y]\n",
		"merge":          "base: &b {b: 1}\n<<: *b\n",
		"dos documentos": "b: 1\n---\nb: 2\n",
		"clave":          "c: 1\n",
		"binario":        "a: [!!binary aGk=]\n",
		"sintaxis":       "a: [\n",
	} {
		if err := Strict([]byte(bad), &c); err == nil {
			t.Errorf("%s: debió fallar", name)
		}
	}
}
