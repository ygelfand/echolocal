package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Talk that never ends sounds the trouble tone until somebody chooses otherwise, which is what every
// device did before the setting existed.
func TestUnendedTalkAlertsByDefault(t *testing.T) {
	if v := load(t).Get().Wake.Slot(0).Unended; v != UnendedAlert {
		t.Errorf("slot 1 unended = %q, want %q", v, UnendedAlert)
	}
	if v := DefaultWakeWord().Unended; v != DefaultUnended || DefaultUnended != UnendedAlert {
		t.Errorf("default unended = %q", v)
	}
}

func TestUnendedIsPerSlot(t *testing.T) {
	st := load(t)
	if err := st.Set().Wake(0).Unended(UnendedQuiet); err != nil {
		t.Fatal(err)
	}
	got := st.Get().Wake
	if v := got.Slot(0).Unended; v != UnendedQuiet {
		t.Errorf("slot 1 unended = %q, want quiet", v)
	}
	if v := got.Slot(1).Unended; v != UnendedAlert {
		t.Errorf("slot 2 unended = %q, want slot 1's change not to reach it", v)
	}
}

// A slot saved before the setting existed has no value for it. Whatever reads it has to take that as the
// default rather than as a choice.
func TestASlotSavedBeforeTheSettingReadsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"wake": {"words": [{"id": "alexa", "threshold": 0.9}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if v := st.Get().Wake.Slot(0).Unended; v != "" {
		t.Errorf("slot 1 unended = %q, want empty", v)
	}
}

func TestUnendedLabels(t *testing.T) {
	values := []Unended{UnendedAlert, UnendedQuiet, UnendedSend}
	got := Labels(values)
	if got[0] != "Alert" || got[1] != "Quiet" || got[2] != "Send to Home Assistant" {
		t.Fatalf("Labels = %q", got)
	}
	for _, want := range values {
		if v, ok := ByLabel(values, want.Label()); !ok || v != want {
			t.Errorf("ByLabel(%q) = %q, %v", want.Label(), v, ok)
		}
	}
}
