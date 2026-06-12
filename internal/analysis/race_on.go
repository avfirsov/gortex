//go:build race

package analysis

// raceEnabled is true when the binary was built with the Go race
// detector (-race). See the matching race_off.go for the rationale.
const raceEnabled = true
