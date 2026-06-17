package review

import (
	"strings"
	"testing"
)

func TestIsConfigLeafLanguage(t *testing.T) {
	for _, lang := range []string{"yaml", "yml", "toml", "ini", "env", "dotenv", "properties", "YAML"} {
		if !IsConfigLeafLanguage(lang) {
			t.Errorf("IsConfigLeafLanguage(%q) = false, want true", lang)
		}
	}
	for _, lang := range []string{"go", "python", "markdown", "json", ""} {
		if IsConfigLeafLanguage(lang) {
			t.Errorf("IsConfigLeafLanguage(%q) = true, want false", lang)
		}
	}
}

func TestIsConfigLeafPath(t *testing.T) {
	for _, p := range []string{"config.yaml", "app.yml", "settings.toml", ".env", "db.ini", "app.properties", "deploy/values.yaml"} {
		if !IsConfigLeafPath(p) {
			t.Errorf("IsConfigLeafPath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"main.go", "README.md", "data.json", "src/index.ts"} {
		if IsConfigLeafPath(p) {
			t.Errorf("IsConfigLeafPath(%q) = true, want false", p)
		}
	}
}

func TestRedactConfigLeaf(t *testing.T) {
	// (a) a config-leaf body with a secret-shaped value has the value withheld
	// while the benign key framing survives.
	secret := "github_token: ghp_0123456789abcdefghijklmnopqrstuvwxyz\n"
	out, hits := RedactConfigLeaf(secret)
	if hits == 0 {
		t.Fatalf("RedactConfigLeaf withheld nothing for a secret-bearing body: %q", out)
	}
	if strings.Contains(out, "ghp_0123456789abcdefghijklmnopqrstuvwxyz") {
		t.Errorf("RedactConfigLeaf left the secret value in the output: %q", out)
	}
	if !strings.Contains(out, "github_token:") {
		t.Errorf("RedactConfigLeaf dropped the benign key framing: %q", out)
	}

	// (b) a benign config body is returned byte-identical with nothing withheld.
	benign := "port: 8080\nname: myapp\ndebug: true\n"
	gotBenign, benignHits := RedactConfigLeaf(benign)
	if benignHits != 0 || gotBenign != benign {
		t.Errorf("RedactConfigLeaf mutated benign config: hits=%d out=%q", benignHits, gotBenign)
	}

	// (c) an obvious placeholder value stays readable.
	placeholder := "password = changeme\n"
	gotPlaceholder, phHits := RedactConfigLeaf(placeholder)
	if phHits != 0 || gotPlaceholder != placeholder {
		t.Errorf("RedactConfigLeaf withheld a placeholder value: hits=%d out=%q", phHits, gotPlaceholder)
	}
}
