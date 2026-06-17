package review

import "strings"

// IsConfigLeafLanguage reports whether a detected parser language code denotes a
// configuration / data-leaf file — one whose literal values (yaml, toml, ini,
// dotenv, properties) routinely carry credentials. It is the language-keyed
// companion to IsConfigLeafPath: a read sink that has a detected language but an
// ambiguous path can still classify the buffer.
func IsConfigLeafLanguage(lang string) bool {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "yaml", "yml", "toml", "ini", "env", "dotenv", "properties", "java-properties":
		return true
	}
	return false
}

// IsConfigLeafPath reports whether a path is a configuration / data-leaf file by
// extension or basename. It is the exported entry point over the internal
// isConfigPath classifier, so the extension / basename set stays a single source
// of truth shared with the review pipeline.
func IsConfigLeafPath(path string) bool {
	return isConfigPath(path)
}

// RedactConfigLeaf withholds secret-shaped values from a config-leaf file body
// while keeping benign keys and values readable, returning the redacted body and
// the count of values withheld. It is the single chokepoint the content-serving
// read sinks call before returning a config-leaf file's contents; it reuses the
// value-shape redactor, so an obvious placeholder (changeme / example / ...) is
// left intact.
func RedactConfigLeaf(body string) (string, int) {
	return RedactSecrets(body)
}
