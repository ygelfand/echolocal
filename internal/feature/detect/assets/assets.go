// Package assets carries the wake words that are the device's own rather than the user's.
package assets

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

// The stop word, from kahrendt/microWakeWord. Embedded because it is not a choice: nothing selects it,
// nothing installs it, and a device without it would have no way to be interrupted.
//
//go:embed stop.tflite
var files embed.FS

// How the model is windowed, from the stop.json of the release it came from.
const (
	StopPhrase      = "Stop"
	StopWindowSize  = 5
	StopFeatureStep = 10
)

// Stop writes the model into dir if it is not already there, and returns the path.
//
// The interpreter takes a path rather than bytes, so the model has to exist as a file. dir is somewhere
// nothing scans for wake words: found by the picker it would be offered as one, and it is not — it has
// no pipeline and its detection means something else entirely.
func Stop(dir string) (string, error) {
	want, err := files.ReadFile("stop.tflite")
	if err != nil {
		return "", fmt.Errorf("assets: reading the embedded stop model: %w", err)
	}

	path := filepath.Join(dir, "stop.tflite")
	if same(path, want) {
		return path, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("assets: %w", err)
	}
	if err := os.WriteFile(path, want, 0o644); err != nil {
		return "", fmt.Errorf("assets: writing the stop model: %w", err)
	}
	return path, nil
}

// same reports whether the file already holds exactly these bytes, so an unchanged model is not
// rewritten on every start and a changed one is not left stale.
func same(path string, want []byte) bool {
	got, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return sha256.Sum256(got) == sha256.Sum256(want)
}
