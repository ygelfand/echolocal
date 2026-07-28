package alsa

import (
	"os"
	"testing"
)

// TestProbeMixer exercises the control ioctls against a real card, which is the only way to know
// the structure layouts match the kernel's: a wrong size is rejected outright, and a wrong stride
// reads a neighbouring value's high half. Reading is enough, and the control device tolerates
// several openers, so this runs safely alongside echod. Set ALSA_PROBE=1 on the device.
func TestProbeMixer(t *testing.T) {
	if os.Getenv("ALSA_PROBE") == "" {
		t.Skip("set ALSA_PROBE=1 on a device with a sound card")
	}

	m, err := OpenMixer(0)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	controls, err := m.Controls()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("elem_value is %d bytes, elem_info %d; %d controls", elemValueSize, elemInfoSize, len(controls))

	var integers, enums, multi int
	for _, c := range controls {
		v, err := m.Get(c)
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		switch c.Type {
		case TypeEnumerated:
			enums++
			if len(v) > 0 && int(v[0]) >= len(c.Items) {
				// Not a stride problem: these all have one value, so no stride is applied. The
				// driver reports an out-of-range item for controls nothing has set.
				t.Logf("%s reports item %d with only %d items", c.Name, v[0], len(c.Items))
			}
		case TypeInteger, TypeBoolean:
			integers++
		}

		// Only a control with several values exercises the stride at all, so these are the
		// interesting ones: with the wrong width the second value comes from the middle of the
		// first.
		if c.Count > 1 {
			multi++
			t.Logf("%-28s type %d count %d value %v", c.Name, c.Type, c.Count, v)
		}
	}
	t.Logf("read %d integer or boolean controls, %d enumerated, %d with several values",
		integers, enums, multi)

	// Spot-check the controls the speaker path depends on.
	for _, name := range []string{"Right Channel Only", "Audio_DacMux_Setting"} {
		c, err := m.Find(name)
		if err != nil {
			t.Logf("%s: %v", name, err)
			continue
		}
		v, err := m.Get(c)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		t.Logf("%-24s type %d count %d value %v items %v", c.Name, c.Type, c.Count, v, c.Items)
	}
}
