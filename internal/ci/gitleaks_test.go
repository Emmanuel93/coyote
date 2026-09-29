package ci

import (
	"fmt"
	"strings"
	"testing"
)

func TestReporteDeGitleaks(t *testing.T) {
	// Un reporte real de gitleaks 8.30 con --redact; aunque el valor viniera
	// sin tapar, coyote no lo lee.
	report := `[{"RuleID":"github-pat","Description":"GitHub PAT","StartLine":3,"EndLine":3,"StartColumn":17,"EndColumn":56,
"Match":"ghp_VALORVISIBLE","Secret":"ghp_VALORVISIBLE","File":"conf/app.txt","SymlinkFile":"","Commit":"f926e1ca143c747cbbd56df8d4a2a0c68d273023",
"Entropy":4.8,"Author":"A","Email":"a@b.c","Date":"2026-09-29T06:52:00Z","Message":"secretos","Tags":[],"Fingerprint":"f926e1ca:conf/app.txt:github-pat:3"}]`
	leaks, total, err := ReadGitleaks(strings.NewReader(report))
	if err != nil || total != 1 || len(leaks) != 1 {
		t.Fatalf("%v %d %v", leaks, total, err)
	}
	l := leaks[0]
	if l.File != "conf/app.txt" || l.Line != 3 || l.Rule != "github-pat" || l.Commit != "f926e1ca143c" {
		t.Fatalf("hallazgo: %+v", l)
	}
	if strings.Contains(fmt.Sprintf("%+v", leaks), "VALORVISIBLE") {
		t.Fatal("el valor nunca se lee")
	}
	for _, bad := range []string{"", "{}", "null", `"[]"`, "[] []", "[]]", "[]x", `[{"RuleID":"x"}]`, `[{"File":"a"}]`, "no es json"} {
		if _, _, err := ReadGitleaks(strings.NewReader(bad)); err == nil {
			t.Errorf("%q debió fallar", bad)
		}
	}
	if leaks, total, err := ReadGitleaks(strings.NewReader("[]")); err != nil || total != 0 || len(leaks) != 0 {
		t.Fatalf("sin hallazgos: %v %d %v", leaks, total, err)
	}
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < maxLeaks+5; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"File":"f%d\u001b[31m.txt","StartLine":1,"RuleID":"r","Commit":"c"}`, i)
	}
	b.WriteString("]")
	leaks, total, err = ReadGitleaks(strings.NewReader(b.String()))
	if err != nil || total != maxLeaks+5 || len(leaks) != maxLeaks || strings.ContainsRune(leaks[0].File, 0x1b) {
		t.Fatalf("tope y limpieza: %d %d %v %q", total, len(leaks), err, leaks[0].File)
	}
	res := DecideGate(GateInput{Leaks: leaks[:1], LeaksTotal: 7})
	if res.Risk != R3 || !strings.Contains(strings.Join(res.Why, " "), "7 hallazgos de gitleaks") {
		t.Fatalf("gitleaks es R3: %+v", res)
	}
}
