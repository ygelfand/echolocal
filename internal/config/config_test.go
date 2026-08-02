package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T) *Store {
	t.Helper()

	st, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return st
}

// A file that was never written reads as untouched, so every default is already in what Get hands
// back and no caller has to supply one.
func TestDefaultsWhenUnset(t *testing.T) {
	got := load(t).Get()

	if got.Speaker.Volume != DefaultVolume {
		t.Errorf("volume = %d, want %d", got.Speaker.Volume, DefaultVolume)
	}
	if got.Microphone.Mixing != DefaultMixing {
		t.Errorf("mixing = %q", got.Microphone.Mixing)
	}
	if got.Microphone.Leveling != DefaultLeveling {
		t.Errorf("leveling = %v", got.Microphone.Leveling)
	}
	if id := got.Wake.Slot(0).ID; id != "" {
		t.Errorf("slot 1 id = %q, want empty so the slot is off", id)
	}
	if v := got.Wake.Slot(1).Threshold; v != DefaultThreshold {
		t.Errorf("slot 2 threshold = %v", v)
	}
	if v := got.Wake.Slot(0).Delivery; v != DeliveryWhole {
		t.Errorf("slot 1 delivery = %q, want the whole file by default", v)
	}
}

// A device nobody has lit comes up dark, and one that was lit comes back the way it was left.
func TestRingLight(t *testing.T) {
	st := load(t)

	if st.Get().Ring.Light.On {
		t.Error("a device nobody has lit comes up on")
	}
	if err := st.Set().Ring().Light(Light{On: true, Brightness: 0.5}); err != nil {
		t.Fatalf("saving the light: %v", err)
	}

	got := st.Get().Ring.Light
	if !got.On || got.Brightness != 0.5 {
		t.Errorf("light = %+v", got)
	}
}

// Delivery and follow-up are per slot, so one assistant can stream while the other fetches.
func TestSlotSettingsAreIndependent(t *testing.T) {
	st := load(t)

	for _, set := range []func() error{
		func() error { return st.Set().Wake(0).ID("glados") },
		func() error { return st.Set().Wake(0).Threshold(0.97) },
		func() error { return st.Set().Wake(0).Tone(ToneRise) },
		func() error { return st.Set().Wake(0).FollowUp(8) },
		func() error { return st.Set().Wake(1).ID("hey_jarvis") },
		func() error { return st.Set().Wake(1).Delivery(DeliveryStream) },
	} {
		if err := set(); err != nil {
			t.Fatalf("setting a slot: %v", err)
		}
	}

	got := st.Get().Wake
	if id := got.Slot(0).ID; id != "glados" {
		t.Errorf("slot 1 id = %q", id)
	}
	if v := got.Slot(0).Threshold; v != 0.97 {
		t.Errorf("slot 1 threshold = %v", v)
	}
	if v := got.Slot(0).Tone; v != ToneRise {
		t.Errorf("slot 1 tone = %q", v)
	}
	if v := got.Slot(0).FollowUp; v != 8 {
		t.Errorf("slot 1 follow-up = %d", v)
	}
	if v := got.Slot(0).Delivery; v != DefaultDelivery {
		t.Errorf("slot 1 delivery = %q, want slot 2's change not to reach it", v)
	}

	if v := got.Slot(1).Delivery; v != DeliveryStream {
		t.Errorf("slot 2 delivery = %q", v)
	}
	if v := got.Slot(1).Threshold; v != DefaultThreshold {
		t.Errorf("slot 2 threshold = %v, want the default", v)
	}
	if v := got.Slot(1).FollowUp; v != DefaultFollowUp {
		t.Errorf("slot 2 follow-up = %d, want off", v)
	}
}

// A zero value has to survive, or turning something off would read as never having been set and the
// default would turn it back on.
func TestZeroValueSticks(t *testing.T) {
	st := load(t)

	if err := st.Set().Speaker().Volume(0); err != nil {
		t.Fatalf("setting the volume: %v", err)
	}
	if v := st.Get().Speaker.Volume; v != 0 {
		t.Errorf("volume = %d after setting 0", v)
	}

	if err := st.Set().Microphone().Leveling(false); err != nil {
		t.Fatalf("setting leveling: %v", err)
	}
	if st.Get().Microphone.Leveling {
		t.Error("leveling came back on after being switched off, so the default won")
	}
}

// Slot 2 can be set before slot 1 ever is, so the list has to grow to reach it without disturbing
// what it skipped.
func TestSettingALaterSlotFirst(t *testing.T) {
	st := load(t)

	if err := st.Set().Wake(1).ID("hey_mycroft"); err != nil {
		t.Fatalf("setting slot 2: %v", err)
	}

	got := st.Get().Wake
	if id := got.Slot(1).ID; id != "hey_mycroft" {
		t.Errorf("slot 2 id = %q", id)
	}
	if id := got.Slot(0).ID; id != "" {
		t.Errorf("slot 1 id = %q, want empty", id)
	}
	if n := len(got.Slots(2)); n != 2 {
		t.Errorf("Slots(2) returned %d", n)
	}
}

// A file that mentions one setting leaves every other one at its default rather than at a zero.
func TestAPartialFileKeepsTheDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"speaker":{"volume":3}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := st.Get()
	if got.Speaker.Volume != 3 {
		t.Errorf("volume = %d, want the file's 3", got.Speaker.Volume)
	}
	if got.Speaker.Resampling != DefaultResampling {
		t.Errorf("resampling = %q, want the default", got.Speaker.Resampling)
	}
	if got.Microphone.Gain != DefaultMicGain {
		t.Errorf("gain = %d, want the default", got.Microphone.Gain)
	}
	if !got.Microphone.Leveling {
		t.Error("leveling came back off, so a missing key beat its default")
	}
	if got.Update.Channel != DefaultChannel {
		t.Errorf("channel = %q, want the default", got.Update.Channel)
	}
}

// A snapshot is a copy: the wake words are a slice, and a caller holding one must not be holding the
// store's own.
func TestGetHandsBackACopy(t *testing.T) {
	st := load(t)
	if err := st.Set().Wake(0).ID("glados"); err != nil {
		t.Fatalf("setting a slot: %v", err)
	}

	got := st.Get()
	got.Wake.Words[0].ID = "meddled"

	if id := st.Get().Wake.Slot(0).ID; id != "glados" {
		t.Errorf("the store now says %q, so Get shared its slice", id)
	}
}

// Start-up options are read the same way as everything else, and are not written to the file.
func TestStartupOptionsAreNotPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	st, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	st.started(Device{Name: "kitchen", Version: "1.2.3", Addr: ":6053"})

	if got := st.Get().Device.Name; got != "kitchen" {
		t.Errorf("name = %q", got)
	}
	if err := st.Set().Speaker().Volume(7); err != nil {
		t.Fatalf("setting the volume: %v", err)
	}
	if got := st.Get().Device.Name; got != "kitchen" {
		t.Errorf("name = %q after a write, want it kept", got)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); strings.Contains(got, "kitchen") {
		t.Errorf("the start-up name reached the file: %s", got)
	}
}

// Settings survive the round trip, or nothing would persist across a restart.
func TestReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	st, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, set := range []func() error{
		func() error { return st.Set().Microphone().Mixing(MixDelaySum) },
		func() error { return st.Set().Speaker().Resampling(ResampleLinear) },
		func() error { return st.Set().Wake(1).Threshold(0.6) },
		func() error { return st.Set().Bluetooth().Proxy(true) },
	} {
		if err := set(); err != nil {
			t.Fatalf("setting: %v", err)
		}
	}

	again, err := Load(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	got := again.Get()

	if got.Microphone.Mixing != MixDelaySum {
		t.Errorf("mixing = %q after reload", got.Microphone.Mixing)
	}
	if got.Speaker.Resampling != ResampleLinear {
		t.Errorf("resampling = %q after reload", got.Speaker.Resampling)
	}
	if v := got.Wake.Slot(1).Threshold; v != 0.6 {
		t.Errorf("slot 2 threshold = %v after reload", v)
	}
	if !got.Bluetooth.Proxy {
		t.Error("the bluetooth proxy came back off")
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
	if err := st.Set().Speaker().Volume(7); err == nil {
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
	if err := st.Set().Speaker().Volume(7); err != nil {
		t.Fatalf("writing a new file failed: %v", err)
	}

	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get().Speaker.Volume; got != 7 {
		t.Errorf("read back volume %d, want 7", got)
	}
}
