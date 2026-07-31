package wake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

// Models Home Assistant offers from its own custom_wake_words directory, which is how a wake word is
// added without touching the device: two files are dropped in that directory, and Home Assistant
// hands them to every satellite that asks for its configuration.
//
// What it offers is the config, with the model beside it, and the hash and size are the model's. So
// this fetches the config to learn the model's name, fetches the model, and refuses it unless both
// match — a wake word that runs on a truncated download would fail in a way nobody could read.

const fetchTimeout = 30 * time.Second

// Have reports whether the offered model is already on disk and is the one being offered.
func Have(dir string, o esphome.ExternalWakeWord) bool {
	data, err := os.ReadFile(modelPath(dir, o.ID))
	if err != nil {
		return false
	}
	return matches(data, o) == nil
}

// Adopt downloads a model unless it is already there, and returns it ready to load.
func Adopt(ctx context.Context, dir string, o esphome.ExternalWakeWord) (Model, error) {
	if Have(dir, o) {
		return load(dir, o.ID), nil
	}

	config, err := get(ctx, o.URL)
	if err != nil {
		return Model{}, fmt.Errorf("wake: config for %s: %w", o.ID, err)
	}

	var m manifest
	if err := json.Unmarshal(config, &m); err != nil {
		return Model{}, fmt.Errorf("wake: config for %s: %w", o.ID, err)
	}
	if m.Model == "" {
		return Model{}, fmt.Errorf("wake: config for %s names no model", o.ID)
	}

	model, err := get(ctx, beside(o.URL, m.Model))
	if err != nil {
		return Model{}, fmt.Errorf("wake: model for %s: %w", o.ID, err)
	}
	if err := matches(model, o); err != nil {
		return Model{}, fmt.Errorf("wake: model for %s: %w", o.ID, err)
	}

	// The offer knows what to call it and what it was trained on; the config knows how to run it.
	m.WakeWord = o.Phrase
	m.TrainedLanguages = o.TrainedLanguages
	m.Model = filepath.Base(modelPath(dir, o.ID))

	if err := write(dir, o.ID, model, m); err != nil {
		return Model{}, err
	}
	return load(dir, o.ID), nil
}

// matches checks a download against what was offered.
func matches(model []byte, o esphome.ExternalWakeWord) error {
	if uint32(len(model)) != o.Size {
		return fmt.Errorf("%d bytes, offered as %d", len(model), o.Size)
	}

	sum := sha256.Sum256(model)
	if got := hex.EncodeToString(sum[:]); got != o.Hash {
		return fmt.Errorf("hash %s, offered as %s", got, o.Hash)
	}
	return nil
}

// write puts the model and its manifest in place, through temporary files: a half-written model that
// looked installed would be adopted on the next start and fail there instead of here.
func write(dir, id string, model []byte, m manifest) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	config, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	for _, f := range []struct {
		path string
		data []byte
	}{
		{modelPath(dir, id), model},
		{filepath.Join(dir, id+".json"), config},
	} {
		tmp := f.path + ".tmp"
		if err := os.WriteFile(tmp, f.data, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, f.path); err != nil {
			return err
		}
	}
	return nil
}

func get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// beside is a sibling of url, which is where Home Assistant serves the model: the whole directory is
// static, so the config's own path with the last segment replaced is the model's.
func beside(url, name string) string {
	cut := len(url) - len(path.Base(url))
	return url[:cut] + name
}

func modelPath(dir, id string) string { return filepath.Join(dir, id+".tflite") }
