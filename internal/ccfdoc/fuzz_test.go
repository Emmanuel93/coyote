package ccfdoc

import "testing"

// FuzzDoc: README.coyote.md y CONTEXT.coyote.md llegan en PRs; leerlos no se cae.
func FuzzDoc(f *testing.F) {
	f.Add("---\nrepo: x\n---\ninv|a|b|-\n")
	f.Add("inv|a\n|||\n---\n")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		for _, name := range []string{ContextFile, ReadmeFile} {
			if d, _ := Parse(name, []byte(s)); d != nil {
				_ = d.Validate()
			}
		}
	})
}
