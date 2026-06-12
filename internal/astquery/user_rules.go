package astquery

import (
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// userRuleFile is the TOML schema for a pluggable domain-extractor
// rule file:
//
//	[[rule]]
//	name        = "feature-flag-register"
//	description = "calls to flags.Register"
//	domain      = "feature_flag"
//	severity    = "info"
//	tags        = ["flags"]
//	[rule.query]
//	go = '(call_expression function: (selector_expression ...)) @match'
type userRuleFile struct {
	Rule []userRule `toml:"rule"`
}

// userRule is one TOML-defined domain-extractor rule. Each becomes an
// astquery Detector registered under Category "domain", so the whole
// rule-execution and `analyze kind=domain` surface is reused.
type userRule struct {
	Name        string `toml:"name"`
	Description string `toml:"description"`
	// Domain labels the rule's purpose — http_route / cli_command /
	// feature_flag / i18n / event_bus / custom. It becomes a tag so
	// `analyze kind=domain tag:<domain>` filters to it.
	Domain   string   `toml:"domain"`
	Severity string   `toml:"severity"`
	Tags     []string `toml:"tags"`
	// Query maps a language ("go", "python", …) to a tree-sitter
	// S-expression. A capture named `match` anchors the row.
	Query map[string]string `toml:"query"`
}

// LoadUserRulesData parses TOML domain-extractor rule definitions and
// registers each as an astquery Detector under Category "domain".
// Returns the number of rules registered. A parse error or an invalid
// rule aborts the load — already-registered rules from the same file
// stay registered.
func LoadUserRulesData(data []byte) (int, error) {
	var f userRuleFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return 0, fmt.Errorf("astquery: parse user rules: %w", err)
	}
	n := 0
	for _, r := range f.Rule {
		d, err := r.toDetector()
		if err != nil {
			return n, err
		}
		RegisterDetector(d)
		n++
	}
	return n, nil
}

// LoadUserRulesFile reads a TOML rule file and registers its rules.
func LoadUserRulesFile(path string) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied config
	if err != nil {
		return 0, fmt.Errorf("astquery: read user rules %s: %w", path, err)
	}
	return LoadUserRulesData(data)
}

// toDetector converts a TOML rule into a registrable Detector. The
// detector name is prefixed `user:` so a user rule can never clobber a
// bundled SAST / hygiene rule.
func (r userRule) toDetector() (*Detector, error) {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		return nil, fmt.Errorf("astquery: user rule missing name")
	}
	langs := map[string]string{}
	for lang, q := range r.Query {
		lang = strings.ToLower(strings.TrimSpace(lang))
		q = strings.TrimSpace(q)
		if lang != "" && q != "" {
			langs[lang] = q
		}
	}
	if len(langs) == 0 {
		return nil, fmt.Errorf("astquery: user rule %q has no query patterns", name)
	}
	domain := strings.TrimSpace(r.Domain)
	if domain == "" {
		domain = "custom"
	}
	severity := strings.TrimSpace(r.Severity)
	if severity == "" {
		severity = "info"
	}
	desc := strings.TrimSpace(r.Description)
	if desc == "" {
		desc = "user-defined " + domain + " rule"
	}
	tags := append([]string{"user-rule", domain}, r.Tags...)
	return &Detector{
		Name:         "user:" + name,
		Description:  desc,
		Severity:     severity,
		Category:     "domain",
		Tags:         tags,
		Languages:    langs,
		ExcludeTests: false,
	}, nil
}
