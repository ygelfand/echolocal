package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMissingFileIsNotAnError(t *testing.T) {
	st, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Load of a missing file: %v", err)
	}
	if got := st.Get(); got.MAC != "" || got.Settings.Volume != nil {
		t.Errorf("Get on a missing file = %+v, want zero", got)
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	st, _ := Load(path)
	if err := st.SetVolume(22); err != nil {
		t.Fatalf("SetVolume: %v", err)
	}
	if err := st.SetMAC("44:65:0d:62:d6:d7"); err != nil {
		t.Fatalf("SetMAC: %v", err)
	}
	if err := st.SetMicMuted(true); err != nil {
		t.Fatalf("SetMicMuted: %v", err)
	}

	reopened, err := Load(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	got := reopened.Get()
	if got.MAC != "44:65:0d:62:d6:d7" {
		t.Errorf("MAC = %q", got.MAC)
	}
	if v := got.Settings.VolumeOr(15); v != 22 {
		t.Errorf("VolumeOr = %d, want 22", v)
	}
	if !got.Settings.MicMutedOr(false) {
		t.Error("MicMutedOr = false, want true")
	}
	// Untouched settings still fall back.
	if !got.Settings.MuteLEDBrightOr(true) {
		t.Error("MuteLEDBrightOr(true) = false, want the default")
	}
}

// A zero value must be distinguishable from unset, which is why the fields are pointers.
func TestZeroIsNotUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	st, _ := Load(path)
	if err := st.SetVolume(0); err != nil {
		t.Fatalf("SetVolume: %v", err)
	}

	reopened, _ := Load(path)
	if v := reopened.Get().Settings.VolumeOr(15); v != 0 {
		t.Errorf("VolumeOr after saving 0 = %d, want 0", v)
	}
}

func TestWriteLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	st, _ := Load(filepath.Join(dir, "state.json"))
	if err := st.SetVolume(1); err != nil {
		t.Fatalf("SetVolume: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("left behind %s", e.Name())
		}
	}
}
