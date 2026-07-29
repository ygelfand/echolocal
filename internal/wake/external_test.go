package wake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// served stands in for Home Assistant's custom_wake_words directory, which it serves statically: the
// config at one path and the model beside it.
func served(t *testing.T, config string, model []byte) (*httptest.Server, Offer) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/esphome/wake_words/test.json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(config))
	})
	mux.HandleFunc("/api/esphome/wake_words/test_model.tflite", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(model)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	sum := sha256.Sum256(model)
	return srv, Offer{
		ID:        "test",
		Phrase:    "Hey Test",
		Languages: []string{"en"},
		Size:      uint32(len(model)),
		Hash:      hex.EncodeToString(sum[:]),
		URL:       srv.URL + "/api/esphome/wake_words/test.json",
	}
}

const testConfig = `{"type":"micro","wake_word":"Ignored","model":"test_model.tflite",
	"micro":{"sliding_window_size":5,"feature_step_size":10}}`

func TestAdoptWritesTheModelAndItsManifest(t *testing.T) {
	dir := t.TempDir()
	model := []byte("this is not really a model")
	_, offer := served(t, testConfig, model)

	m, err := Adopt(context.Background(), dir, offer)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}

	if m.ID != "test" {
		t.Errorf("id = %q", m.ID)
	}
	// The offer names it, not the config: Home Assistant's own label is what the user picked.
	if m.Phrase != "Hey Test" {
		t.Errorf("phrase = %q, want the offered one", m.Phrase)
	}

	got, err := os.ReadFile(filepath.Join(dir, "test.tflite"))
	if err != nil {
		t.Fatalf("model was not written: %v", err)
	}
	if string(got) != string(model) {
		t.Error("the written model is not what was served")
	}
	if _, err := os.Stat(filepath.Join(dir, "test.json")); err != nil {
		t.Errorf("manifest was not written: %v", err)
	}
}

// A truncated or swapped model must be refused: running one would fail later, somewhere harder to
// read than here.
func TestAdoptRefusesAWrongHash(t *testing.T) {
	dir := t.TempDir()
	_, offer := served(t, testConfig, []byte("the model"))
	offer.Hash = "0000000000000000000000000000000000000000000000000000000000000000"

	if _, err := Adopt(context.Background(), dir, offer); err == nil {
		t.Fatal("a model with the wrong hash was adopted")
	}
	if _, err := os.Stat(filepath.Join(dir, "test.tflite")); err == nil {
		t.Error("a refused model was left on disk")
	}
}

func TestAdoptRefusesAWrongSize(t *testing.T) {
	dir := t.TempDir()
	_, offer := served(t, testConfig, []byte("the model"))
	offer.Size = 99

	if _, err := Adopt(context.Background(), dir, offer); err == nil {
		t.Fatal("a model of the wrong size was adopted")
	}
}

func TestAdoptNeedsTheConfigToNameAModel(t *testing.T) {
	dir := t.TempDir()
	_, offer := served(t, `{"type":"micro","wake_word":"Hey"}`, []byte("the model"))

	if _, err := Adopt(context.Background(), dir, offer); err == nil {
		t.Fatal("a config naming no model was accepted")
	}
}

// Have is what keeps a device from downloading the same model on every reconnect.
func TestHaveRecognisesWhatIsInstalled(t *testing.T) {
	dir := t.TempDir()
	model := []byte("this is not really a model")
	_, offer := served(t, testConfig, model)

	if Have(dir, offer) {
		t.Error("an empty directory reports having the model")
	}
	if _, err := Adopt(context.Background(), dir, offer); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !Have(dir, offer) {
		t.Error("an adopted model is not recognised")
	}

	// The same id offered with different contents is a different model.
	offer.Hash = "1111111111111111111111111111111111111111111111111111111111111111"
	if Have(dir, offer) {
		t.Error("a model with another hash counts as installed")
	}
}
