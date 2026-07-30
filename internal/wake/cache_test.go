package wake

import (
	"os"
	"path/filepath"
	"testing"
)

// put writes a model and, unless the manifest is empty, a sidecar beside it.
func put(t *testing.T, dir, id, manifest string, bytes int) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, id+".tflite"), make([]byte, bytes), 0o644); err != nil {
		t.Fatal(err)
	}
	if manifest == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCachedIsWhatNoSlotWants(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "glados", "", 100)
	put(t, dir, "hey_jarvis", `{"wake_word":"Hey Jarvis"}`, 200)
	put(t, dir, "wall-e", "", 400)

	stale, bytes := Cached(dir, []string{"hey_jarvis"})
	if len(stale) != 2 {
		t.Fatalf("%d cached, want the two nothing is listening for", len(stale))
	}
	// The manifest counts too, so only the two unused models and neither of their sizes is the
	// selected one's.
	if bytes != 500 {
		t.Errorf("cached %d bytes, want 500", bytes)
	}

	for _, m := range stale {
		if m.ID == "hey_jarvis" {
			t.Error("the model a slot is listening for was counted as cache")
		}
	}
}

func TestPurgeLeavesWhatIsInUse(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "glados", `{"wake_word":"GLaDOS"}`, 100)
	put(t, dir, "hey_jarvis", "", 200)

	gone, freed := Purge(dir, []string{"hey_jarvis"})
	if gone != 1 {
		t.Errorf("deleted %d models, want 1", gone)
	}
	if freed == 0 {
		t.Error("freed nothing")
	}

	if _, err := os.Stat(filepath.Join(dir, "hey_jarvis.tflite")); err != nil {
		t.Errorf("the model in use was deleted: %v", err)
	}
	for _, name := range []string{"glados.tflite", "glados.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived the purge", name)
		}
	}
}

// Nothing selected means everything is cache, which is the whole point of the button: a device left
// with no wake word should not be holding a directory of models.
func TestPurgeWithNothingSelected(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "glados", "", 100)
	put(t, dir, "wall-e", "", 100)

	if gone, _ := Purge(dir, nil); gone != 2 {
		t.Errorf("deleted %d models, want both", gone)
	}

	// An empty directory is not an error: a device with no models is the state a new one starts in, and
	// it still has to reach Home Assistant to be offered its first.
	left, err := Installed(dir)
	if err != nil {
		t.Errorf("Installed on an emptied directory: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("Installed found %d models in an emptied directory", len(left))
	}
}
