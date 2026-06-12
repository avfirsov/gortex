//go:build race

package embedding

// raceEnabled is true when the binary was built with the Go race
// detector. Used to skip tests whose failure mode is an upstream
// library race we don't control (go-huggingface/hub.DownloadFilesCtx),
// rather than a real bug in our own code.
const raceEnabled = true
