package glob

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"**/respaldos/**", "respaldos/dump.sql", true},
		{"**/respaldos/**", "a/b/respaldos/c/d.sql", true},
		{"**/respaldos/**", "a/respaldosx/d.sql", false},
		{"*.dump", "db/prod.dump", true},
		{"*.dump", "db/prod.dump.txt", false},
		{".github/workflows/*.yml", ".github/workflows/ci.yml", true},
		{".github/workflows/*.yml", ".github/workflows/sub/ci.yml", false},
		{"docs/**", "docs/specs/ccf-v1.md", true},
		{"docs/**", "docs", true},
		{"**/*.md", "README.md", true},
		{"**/*.md", "a/b/c.md", true},
		{"**/*.md", "a/b/c.mdx", false},
		{"**/Claude outputs/**", "x/Claude outputs/informe.pdf", true},
		{"internal/{cli,ccf}/**", "internal/ccf/ccf.go", true},
		{"internal/{cli,ccf}/**", "internal/glob/glob.go", false},
		{"README.md", "README.md", true},
		{"README.md", "docs/README.md", true},
		{"src/?.ts", "src/a.ts", true},
		{"src/?.ts", "src/ab.ts", false},
		{"**/*.{sql,{bak,dump}}", "db/prod.dump", true},
		{"**/*.{sql,{bak,dump}}", "db/prod.txt", false},
		{"backups/", "backups/x.sql", true},
		{"backups/", "a/backups/b/x.sql", true},
		{"backups/", "backups.sql", false},
		{"data/raw/", "data/raw/x.csv", true},
		{"data/raw/", "other/data/raw/x.csv", false},
		{"{a,b", "a", true},
		{"a}b", "a}b", true},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, se esperaba %v", c.pattern, c.path, got, c.want)
		}
	}
}
