//go:build board_checkers || board_cronos || board_crown || board_athena

package speaker

import "log/slog"

// As audio_device_<name>.xml sets it up.
type showAmp struct {
	name   string
	marker string
	init   []kctl
	on     []kctl
	off    []kctl
	path   map[Output][]kctl
}

var showAmps = []showAmp{
	{
		name:   "rt5616",
		marker: "DAC1 Playback Volume",
		init: []kctl{
			{name: AmpSwitch, value: "On"},
			{name: "Audio_I2S0dl1_hd_Switch", value: "On"},
			{name: "DAC MIXL INF1 Switch", level: 1},
			{name: "DAC MIXR INF1 Switch", level: 1},
			{name: "Stereo DAC MIXL DAC L1 Switch", level: 1},
			{name: "Stereo DAC MIXL DAC R1 Switch", level: 1},
			{name: "Stereo DAC MIXR DAC L1 Switch", level: 1},
			{name: "Stereo DAC MIXR DAC R1 Switch", level: 1},
			{name: "LOUT MIX DAC L1 Switch", level: 0},
			{name: "LOUT MIX DAC R1 Switch", level: 0},
			{name: "LOUT MIX OUTVOL L Switch", level: 1},
			{name: "LOUT MIX OUTVOL R Switch", level: 1},
			{name: "DAC1 Playback Volume", level: 173},
			{name: "OUT Playback Volume", level: 29},
			{name: "OUT MIXL DAC L1 Switch", level: 1},
			{name: "OUT MIXR DAC R1 Switch", level: 1},
			{name: "OUT Channel Switch", level: 1},
			{name: "OUT Playback Switch", level: 1},
			{name: "HPO MIX DAC1 Switch", level: 0},
			{name: "HPO MIX HPVOL Switch", level: 1},
		},
		on: []kctl{
			{name: "Stereo DAC MIXL DAC R1 Switch", level: 1},
			{name: "Stereo DAC MIXR DAC L1 Switch", level: 1},
			{name: "LOUT MIX OUTVOL L Switch", level: 1},
			{name: "LOUT MIX OUTVOL R Switch", level: 1},
			{name: "OUT Playback Switch", level: 1},
			{name: AmpSwitch, value: "Off"},
		},
		off: []kctl{
			{name: AmpSwitch, value: "On"},
			{name: "LOUT MIX OUTVOL L Switch", level: 0},
			{name: "LOUT MIX OUTVOL R Switch", level: 0},
			{name: "OUT Playback Switch", level: 0},
		},
		path: map[Output][]kctl{
			OutputSpeaker:   {{name: "HPVOL Playback Switch", level: 0}},
			OutputHeadphone: {{name: "HPVOL Playback Switch", level: 1}},
		},
	},
	{
		name:   "tas5805m",
		marker: "Master TAS5805m Playback Volume A",
		init: []kctl{
			{name: AmpSwitch, value: "Off"},
			{name: "Audio_I2S0dl1_hd_Switch", value: "On"},
			{name: "Master TAS5805m Playback Volume A", level: 110},
		},
	},
	{
		name:   "tas5805m-amplifier",
		marker: "Master TAS5805m Playback Volume",
		init: []kctl{
			{name: "Audio_I2S0dl1_hd_Switch", value: "On"},
			{name: "Master TAS5805m Playback Volume", level: 100},
		},
		on:  []kctl{{name: AmpSwitch, value: "On"}},
		off: []kctl{{name: AmpSwitch, value: "Off"}},
	},
	{
		name:   "max98396",
		marker: "Speaker Volume A",
		init: []kctl{
			// No AmpSwitch: on cronos it drives the MAX98396's reset, and writing it wipes the
			// setup the codec driver did at probe, leaving the speaker silent until a reboot.
			{name: "Audio_I2S0dl1_hd_Switch", value: "On"},
			{name: "Digital Volume A", level: 127},
			{name: "Speaker Volume A", level: 8},
			{name: "Speaker Safe Mode A", level: 0}, // BOOL on this kernel, not an enum
		},
	},
}

func whichShowAmp(has func(control string) bool) (showAmp, bool) {
	for _, a := range showAmps {
		if has(a.marker) {
			return a, true
		}
	}
	return showAmp{}, false
}

func GainRange() (lo, hi int, ok bool) { return 0, 0, false }

func sequencesFor(has func(control string) bool) sequences {
	a, ok := whichShowAmp(has)
	if !ok {
		slog.Error("the speaker amplifier is none this build knows")
		return sequences{}
	}
	slog.Info("speaker amplifier", "chip", a.name)
	return sequences{init: a.init, on: a.on, off: a.off, path: a.path, close: a.off}
}
