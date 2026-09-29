package hub

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRefDesdeProjectYAML(t *testing.T) {
	cases := []struct {
		in   string
		want Ref
		err  bool
	}{
		{`hub: ""`, Ref{}, false},
		{`hub:`, Ref{}, false},
		{`hub: ../acme-hub`, Ref{Path: "../acme-hub"}, false},
		{`hub: { path: ~/Documents/acme-hub, ref: v3 }`, Ref{Path: "~/Documents/acme-hub", Ref: "v3"}, false},
		{`hub: { path: ../h, rama: main }`, Ref{}, true},
		{`hub: [a, b]`, Ref{}, true},
		{`hub: { path: [a] }`, Ref{}, true},
	}
	for _, c := range cases {
		var p struct {
			Hub Ref `yaml:"hub"`
		}
		err := yaml.Unmarshal([]byte(c.in), &p)
		if (err != nil) != c.err || (!c.err && p.Hub != c.want) {
			t.Errorf("%s: %+v, %v", c.in, p.Hub, err)
		}
	}
	for _, bad := range []Ref{{Path: "https://github.com/acme/hub"}, {Path: "git@github.com:acme/hub.git"}, {Ref: "main"},
		{Path: "../h", Ref: "-c"}, {Path: "../h", Ref: "main..dev"}, {Path: "../h", Ref: "main^{tree}"}, {Path: "../h", Ref: "main:x"}} {
		if bad.Validate() == nil {
			t.Errorf("%+v no es válido", bad)
		}
	}
	out, _ := yaml.Marshal(struct {
		Hub Ref `yaml:"hub"`
	}{Ref{Path: "../h", Ref: "v1"}})
	if !strings.Contains(string(out), "ref: v1") {
		t.Fatalf("se escribe como mapa con ref: %s", out)
	}
}

func TestHubYAML(t *testing.T) {
	c, err := Parse([]byte("version: 1\norg: acme\nadmins: [ana, \"@Ana\", \"@luis\"]\nbudgets: { monthly_usd: 120 }\nprojects:\n  - { name: shop, path: ../shop, repo: acme/shop }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Admins, ",") != "@ana,@luis" || !c.IsAdmin("ana") || !c.IsAdmin("@LUIS") || c.IsAdmin("@beto") {
		t.Fatalf("admins normalizados: %v", c.Admins)
	}
	for _, bad := range []string{
		"version: 2\norg: acme\n",
		"version: 1\norg: \"\"\n",
		"version: 1\norg: acme\nadmin: [ana]\n",
		"version: 1\norg: acme\nadmins: [\"ana smith\"]\n",
		"version: 1\norg: acme\nbudgets: { monthly_usd: -1 }\n",
		"version: 1\norg: acme\nprojects: [{ name: shop }, { name: Shop }]\n",
		"version: 1\norg: acme\nprojects: [{ name: shop, repo: \"acme/shop; rm\" }]\n",
		"version: 1\norg: acme\nprojects: [{ name: shop, path: \"https://x/y\" }]\n",
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("debió fallar: %q", bad)
		}
	}
}

func TestCleanRel(t *testing.T) {
	for _, ok := range []string{"coyote/hub.yaml", "coyote/standards/../standards/rules.yaml", "domains/pagos.md"} {
		if _, err := CleanRel(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "../x", "/etc/passwd", "coyote/../../x", "a:b", "a\nb"} {
		if _, err := CleanRel(bad); err == nil {
			t.Errorf("%q sale del hub", bad)
		}
	}
}
