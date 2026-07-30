package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func load(t *testing.T) *Store {
	t.Helper()

	st, err := Load(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return st
}

// A file that was never written reads as untouched, so every default applies.
func TestDefaultsWhenUnset(t *testing.T) {
	got := load(t).Get()

	if v := got.Speaker.VolumeOr(15); v != 15 {
		t.Errorf("VolumeOr(15) = %d", v)
	}
	if v := got.Microphone.MixingOr(MixDelaySum); v != MixDelaySum {
		t.Errorf("MixingOr = %q", v)
	}
	if id := got.Wake.WordID(0); id != "" {
		t.Errorf("WordID(0) = %q, want empty so the slot is off", id)
	}
	if v := got.Wake.Slot(1).ThresholdOr(DefaultThreshold); v != DefaultThreshold {
		t.Errorf("ThresholdOr = %v", v)
	}
	if v := got.Wake.Slot(0).DeliveryOr(DefaultDelivery); v != DeliveryWhole {
		t.Errorf("DeliveryOr = %q, want the whole file by default", v)
	}
}

// Delivery is per slot, so one assistant can stream while the other fetches.
func TestDeliveryIsPerSlot(t *testing.T) {
	st := load(t)

	if err := st.SetWakeDelivery(1, DeliveryStream); err != nil {
		t.Fatalf("SetWakeDelivery: %v", err)
	}

	got := st.Get().Wake
	if v := got.Slot(1).DeliveryOr(DefaultDelivery); v != DeliveryStream {
		t.Errorf("slot 2 delivery = %q", v)
	}
	if v := got.Slot(0).DeliveryOr(DefaultDelivery); v != DeliveryWhole {
		t.Errorf("slot 1 delivery = %q, want the default untouched", v)
	}
}

// Follow-up time is per slot too, and zero is off rather than unset.
func TestFollowUpIsPerSlot(t *testing.T) {
	st := load(t)

	if err := st.SetWakeFollowUp(0, 8); err != nil {
		t.Fatalf("SetWakeFollowUp: %v", err)
	}

	got := st.Get().Wake
	if v := got.Slot(0).FollowUpOr(DefaultFollowUp); v != 8 {
		t.Errorf("slot 1 follow-up = %d", v)
	}
	if v := got.Slot(1).FollowUpOr(DefaultFollowUp); v != 0 {
		t.Errorf("slot 2 follow-up = %d, want off", v)
	}
}

// A zero value has to survive, or turning something off would read as never having been set and the
// default would turn it back on.
func TestZeroValueSticks(t *testing.T) {
	st := load(t)
	if err := st.SetSpeakerVolume(0); err != nil {
		t.Fatalf("SetSpeakerVolume: %v", err)
	}

	if v := st.Get().Speaker.VolumeOr(15); v != 0 {
		t.Errorf("VolumeOr(15) = %d after setting 0, want 0", v)
	}
}

// Each slot is tuned on its own: two wake words can be different models on different engines, which
// score on different scales, so a threshold set for one must not follow the other.
func TestSlotsAreIndependent(t *testing.T) {
	st := load(t)

	for _, set := range []func() error{
		func() error { return st.SetWakeWord(0, "glados") },
		func() error { return st.SetWakeThreshold(0, 0.97) },
		func() error { return st.SetWakeTone(0, ToneRise) },
		func() error { return st.SetWakeWord(1, "hey_jarvis") },
	} {
		if err := set(); err != nil {
			t.Fatalf("setting a slot: %v", err)
		}
	}

	got := st.Get().Wake
	if id := got.WordID(0); id != "glados" {
		t.Errorf("WordID(0) = %q, want glados", id)
	}
	if v := got.Slot(0).ThresholdOr(DefaultThreshold); v != 0.97 {
		t.Errorf("ThresholdOr(0) = %v, want 0.97", v)
	}
	if v := got.Slot(0).ToneOr(DefaultTone); v != ToneRise {
		t.Errorf("ToneOr(0) = %q, want %q", v, ToneRise)
	}

	if id := got.WordID(1); id != "hey_jarvis" {
		t.Errorf("WordID(1) = %q, want hey_jarvis", id)
	}
	if v := got.Slot(1).ThresholdOr(DefaultThreshold); v != DefaultThreshold {
		t.Errorf("ThresholdOr(1) = %v, want the default", v)
	}
	if v := got.Slot(1).ToneOr(DefaultTone); v != DefaultTone {
		t.Errorf("ToneOr(1) = %q, want the default", v)
	}
}

// Slot 2 can be set before slot 1 ever is, so the list has to grow to reach it without disturbing
// what it skipped.
func TestSettingALaterSlotFirst(t *testing.T) {
	st := load(t)

	if err := st.SetWakeWord(1, "hey_mycroft"); err != nil {
		t.Fatalf("SetWakeWord: %v", err)
	}

	got := st.Get().Wake
	if id := got.WordID(1); id != "hey_mycroft" {
		t.Errorf("WordID(1) = %q", id)
	}
	if id := got.WordID(0); id != "" {
		t.Errorf("WordID(0) = %q, want empty", id)
	}
	if n := len(got.Slots(2)); n != 2 {
		t.Errorf("Slots(2) returned %d", n)
	}
}

// Settings survive the round trip, or nothing would persist across a restart.
func TestReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	st, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := st.SetMicMixing(MixCenter); err != nil {
		t.Fatalf("SetMicMixing: %v", err)
	}
	if err := st.SetSpeakerResampling(ResampleLinear); err != nil {
		t.Fatalf("SetSpeakerResampling: %v", err)
	}
	if err := st.SetWakeThreshold(1, 0.6); err != nil {
		t.Fatalf("SetWakeThreshold: %v", err)
	}

	again, err := Load(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	got := again.Get()

	if v := got.Microphone.MixingOr(MixDelaySum); v != MixCenter {
		t.Errorf("MixingOr = %q after reload", v)
	}
	if v := got.Speaker.ResamplingOr(ResampleSinc); v != ResampleLinear {
		t.Errorf("ResamplingOr = %q after reload", v)
	}
	if v := got.Wake.Slot(1).ThresholdOr(DefaultThreshold); v != 0.6 {
		t.Errorf("ThresholdOr = %v after reload", v)
	}
}

// Labels are what Home Assistant sends back, so the mapping has to round trip.
func TestByLabelRoundTrips(t *testing.T) {
	values := []Mixing{MixCenter, MixDelaySum}

	if got := Labels(values); got[0] != "Center mic" || got[1] != "Delay and sum" {
		t.Fatalf("Labels = %q", got)
	}
	for _, want := range values {
		got, ok := ByLabel(values, want.Label())
		if !ok || got != want {
			t.Errorf("ByLabel(%q) = %q, %v", want.Label(), got, ok)
		}
	}
	if _, ok := ByLabel(values, "Beamformer"); ok {
		t.Error("ByLabel accepted a value this build does not offer")
	}
}

// A file that could not be read must not be written over. Everything here is one document, so a store
// that fell back to defaults would replace settings it merely failed to parse with settings that have
// nothing in them — and the reboot that truncated the file would take the contents with it for good.
func TestAnUnreadableFileIsNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	// Truncated, which is what losing power part way through a write leaves behind.
	if err := os.WriteFile(path, []byte(`{"speaker": {"volu`), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := Load(path)
	if err == nil {
		t.Fatal("loading a truncated file succeeded, want an error")
	}

	if err := st.SetSpeakerVolume(7); err == nil {
		t.Error("writing over an unreadable file succeeded, want a refusal")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(after), `{"speaker": {"volu`; got != want {
		t.Errorf("the file is now %q, want it untouched as %q", got, want)
	}
}

// A missing file is a different thing from an unreadable one: there is nothing to lose, and the first
// change is what creates it.
func TestAMissingFileIsWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	st, err := Load(path)
	if err != nil {
		t.Fatalf("loading a missing file failed: %v", err)
	}
	if err := st.SetSpeakerVolume(7); err != nil {
		t.Fatalf("writing a new file failed: %v", err)
	}

	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get().Speaker.VolumeOr(0); got != 7 {
		t.Errorf("read back volume %d, want 7", got)
	}
}
