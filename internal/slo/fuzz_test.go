package slo

import "testing"

// FuzzQuery: las consultas de un SLO llegan en PRs; el lector no se cae.
func FuzzQuery(f *testing.F) {
	for _, s := range []string{`sum(rate(http_requests_total{code=~"5.."}[{{.window}}]))`, `a offset 5m`, `x[5m:1m]`, `"a\"b"`, "`raw`", `{__name__="x"}`, `# c`, `(`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, q string) { _ = scanQuery(q) })
}

// FuzzSpec: un archivo de SLOs de un PR se lee sin caerse.
func FuzzSpec(f *testing.F) {
	f.Add([]byte("version: 1\nservice: x\nslos: []\n"))
	f.Add([]byte("{"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if s, err := Parse(data); err == nil && s != nil {
			_, _ = Rules(s)
		}
	})
}
