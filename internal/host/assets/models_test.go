package assets_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ygelfand/echolocal/internal/host/assets"
	"github.com/ygelfand/echolocal/internal/lib/wake"
)

// The three the reference satellite ships. A device with none of them cannot offer a wake word until
// Home Assistant hosts one, which is the state this exists to avoid.
var want = []string{"hey_jarvis", "hey_mycroft", "okay_nabu"}

func TestModelsAreTheOnesWeMeanToShip(t *testing.T) {
	models, err := assets.Models()
	if err != nil {
		t.Fatal(err)
	}

	var ids []string
	for _, m := range models {
		ids = append(ids, m.ID)
		if len(m.Model) == 0 || len(m.Manifest) == 0 {
			t.Errorf("%s: model %d bytes, manifest %d bytes", m.ID, len(m.Model), len(m.Manifest))
		}
	}
	slices.Sort(ids)

	if !slices.Equal(ids, want) {
		t.Errorf("ships %v, want %v", ids, want)
	}
}

// Every manifest has to name its model file and its phrase: the phrase is what Home Assistant shows,
// and a manifest naming a file that is not beside it loads nothing.
func TestManifestsNameTheirModelAndPhrase(t *testing.T) {
	models, err := assets.Models()
	if err != nil {
		t.Fatal(err)
	}

	for _, m := range models {
		var manifest struct {
			WakeWord string `json:"wake_word"`
			Model    string `json:"model"`
			Type     string `json:"type"`
		}
		if err := json.Unmarshal(m.Manifest, &manifest); err != nil {
			t.Errorf("%s: %v", m.ID, err)
			continue
		}
		if manifest.WakeWord == "" {
			t.Errorf("%s: no phrase", m.ID)
		}
		if manifest.Model != m.ID+".tflite" {
			t.Errorf("%s: manifest names %q", m.ID, manifest.Model)
		}
		if manifest.Type != "micro" {
			t.Errorf("%s: type %q, want micro", m.ID, manifest.Type)
		}
	}
}

// What the installer writes has to be what the engine then reads: same directory, same names, and
// recognised as microWakeWord from the model itself rather than from the manifest.
func TestWhatIsShippedIsWhatTheEngineLoads(t *testing.T) {
	models, err := assets.Models()
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	for _, m := range models {
		write(t, filepath.Join(dir, m.ID+".tflite"), m.Model)
		write(t, filepath.Join(dir, m.ID+".json"), m.Manifest)
	}

	installed, err := wake.Installed(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != len(want) {
		t.Fatalf("Installed found %d models, want %d", len(installed), len(want))
	}

	for _, m := range installed {
		if m.Kind != wake.KindMicroWakeWord {
			t.Errorf("%s loads as %s, want microwakeword", m.ID, m.Kind)
		}
		if m.Phrase == "" || m.Phrase == m.ID {
			t.Errorf("%s: phrase %q did not come from the manifest", m.ID, m.Phrase)
		}
		if m.Config.SlidingWindowSize == 0 || m.Config.FeaturesStepMs == 0 {
			t.Errorf("%s: window %d step %d", m.ID, m.Config.SlidingWindowSize, m.Config.FeaturesStepMs)
		}
	}
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
