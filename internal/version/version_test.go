package version

import (
	"strings"
	"testing"
)

func release(t *testing.T, v string) {
	t.Helper()
	oldV, oldC := Version, Commit
	Version, Commit = v, "abc1234"
	t.Cleanup(func() { Version, Commit = oldV, oldC })
}

func TestMajorMinor(t *testing.T) {
	for v, want := range map[string][2]int{"v1.0.0": {1, 0}, "1.2": {1, 2}, "v1.0.0-dev.1": {1, 0}, "v0.7.0-3-gabc1234": {0, 7}, "2.10.3+meta": {2, 10}} {
		if m, n, ok := MajorMinor(v); !ok || m != want[0] || n != want[1] {
			t.Errorf("MajorMinor(%q) = %d.%d %v", v, m, n, ok)
		}
	}
	for _, v := range []string{"", "1", "dev", "1.x", "1.2.3.4", "-1.0"} {
		if _, _, ok := MajorMinor(v); ok {
			t.Errorf("MajorMinor(%q) no debería entenderse", v)
		}
	}
}

func TestCheckRequires(t *testing.T) {
	release(t, "v1.0.0")
	for c, ok := range map[string]bool{"": true, ">=1.0": true, ">=0.7": true, ">= 1.0": true, ">=1.1": false, ">=2.0": false} {
		if err := CheckRequires(c); (err == nil) != ok {
			t.Errorf("CheckRequires(%q) con v1.0.0: %v", c, err)
		}
	}
	if err := CheckRequires(">=1.1"); err == nil || !strings.Contains(err.Error(), "pide coyote >=1.1 y tienes v1.0.0") {
		t.Errorf("mensaje: %v", err)
	}
	for _, bad := range []string{"1.0", "~1.0", ">1.0", ">=uno"} {
		if err := CheckRequires(bad); err == nil || !strings.Contains(err.Error(), "usa >=MAYOR.MENOR") {
			t.Errorf("%q debería rechazarse por su forma: %v", bad, err)
		}
	}
	if Requires() != ">=1.0" {
		t.Errorf("Requires: %q", Requires())
	}
	release(t, "v1.0.0-dev.1")
	if err := CheckRequires(">=1.0"); err != nil || Requires() != ">=1.0" {
		t.Errorf("un 1.0.0-dev ya es de la línea 1.0: %v %q", err, Requires())
	}
	Commit = "none"
	if err := CheckRequires(">=9.0"); err != nil || Requires() != "" {
		t.Errorf("un build sin versión no se compara ni escribe requires: %v %q", err, Requires())
	}
}
