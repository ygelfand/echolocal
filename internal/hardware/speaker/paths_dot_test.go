//go:build !board_checkers && !board_cronos && !board_crown && !board_athena

package speaker

import (
	"slices"
	"testing"
)

var dacSwitches = []string{"HPL Output Mixer L_DAC Switch", "HPR Output Mixer R_DAC Switch"}

func TestTheDotRoutesTheDACAtStartup(t *testing.T) {
	for _, name := range dacSwitches {
		on := func(k kctl) bool { return k.name == name && k.level == 1 }
		if !slices.ContainsFunc(initSequence, on) {
			t.Errorf("the init sequence leaves %q off", name)
		}
	}
}

func TestNoDotSequenceTurnsTheDACOff(t *testing.T) {
	seqs := sequencesFor(nil)
	all := map[string][]kctl{"on": seqs.on, "off": seqs.off, "close": seqs.close}
	for out, seq := range seqs.path {
		all["path "+string(out)] = seq
	}
	for out, seq := range seqs.leave {
		all["leave "+string(out)] = seq
	}
	for which, seq := range all {
		for _, k := range seq {
			if slices.Contains(dacSwitches, k.name) && k.level != 1 {
				t.Errorf("the %s sequence sets %q to %d", which, k.name, k.level)
			}
		}
	}
}

func radarControls(c string) bool { return c == mfpMute }

func lastWrite(seq []kctl, name string) (kctl, bool) {
	for i := len(seq) - 1; i >= 0; i-- {
		if seq[i].name == name {
			return seq[i], true
		}
	}
	return kctl{}, false
}

func TestBiscuitNeverTouchesRadarsGates(t *testing.T) {
	seqs := sequencesFor(func(string) bool { return false })
	all := append(slices.Clone(seqs.init), seqs.on...)
	all = append(all, seqs.off...)
	for _, seq := range seqs.path {
		all = append(all, seq...)
	}
	for _, k := range all {
		if k.name == mfpMute || k.name == speakerAmp || k.name == outputMux {
			t.Errorf("a biscuit sequence writes radar's %q", k.name)
		}
	}
}

// The driver leaves MFP2 high, which holds the amplifier off: radar plays nothing until it drops.
func TestRadarOpensTheMFPGateOnStartAndOnEveryPath(t *testing.T) {
	seqs := sequencesFor(radarControls)
	which := map[string][]kctl{"init": seqs.init}
	for out, seq := range seqs.path {
		which["path "+string(out)] = seq
	}
	for name, seq := range which {
		if k, ok := lastWrite(seq, mfpMute); !ok || k.value != "Off" {
			t.Errorf("the %s sequence leaves %q at %+v", name, mfpMute, k)
		}
	}
}

func TestRadarStartsWithTheDriverOff(t *testing.T) {
	seqs := sequencesFor(radarControls)
	if k, ok := lastWrite(seqs.init, speakerAmp); !ok || k.value != "Off" {
		t.Errorf("init leaves %q at %+v, want Off until the codec settles", speakerAmp, k)
	}
	if k, ok := lastWrite(seqs.on, speakerAmp); !ok || k.value != "On" {
		t.Errorf("the on sequence leaves %q at %+v", speakerAmp, k)
	}
}

func TestRadarMuxFollowsTheOutput(t *testing.T) {
	seqs := sequencesFor(radarControls)
	for out, want := range map[Output]string{
		OutputSpeaker:   "Speaker",
		OutputHeadphone: "Headphone",
	} {
		if k, ok := lastWrite(seqs.path[out], outputMux); !ok || k.value != want {
			t.Errorf("the %s path sets %q to %+v, want %s", out, outputMux, k, want)
		}
	}
}

// Ext_Speaker_Amp_Switch feeds the line-out as well on radar: turning the speaker off must not
// silence a plugged-in headphone.
func TestRadarSpeakerOffLeavesTheLineOut(t *testing.T) {
	seqs := sequencesFor(radarControls)
	if _, ok := lastWrite(seqs.off, AmpSwitch); ok {
		t.Errorf("the off sequence writes %q", AmpSwitch)
	}
	if k, ok := lastWrite(seqs.path[OutputHeadphone], AmpSwitch); !ok || k.value != "On" {
		t.Errorf("the headphone path leaves %q at %+v", AmpSwitch, k)
	}
}

func TestRadarRoutesTheDACLikeBiscuit(t *testing.T) {
	seqs := sequencesFor(radarControls)
	for _, name := range dacSwitches {
		if k, ok := lastWrite(seqs.init, name); !ok || k.level != 1 {
			t.Errorf("radar's init leaves %q at %+v", name, k)
		}
	}
}
