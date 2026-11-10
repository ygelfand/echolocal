//go:build !board_checkers && !board_cronos && !board_crown && !board_athena

package speaker

import (
	"log/slog"
	"slices"
)

var initSequence = []kctl{
	{name: "HPL Output Mixer L_DAC Switch", level: 1},
	{name: "HPR Output Mixer R_DAC Switch", level: 1},
	{name: AmpSwitch, value: "Off"},
	{name: "Audio_DacMux_Setting", value: "On"},
	{name: "Ignore Ramp Up", value: "Off"},
	{name: driverGain, level: 0},
	{name: "biquad coefficients", blob: speakerEQ},
}

// The vendor's tuning: six unity blocks and one filter; the coefficients read back as zeros until written.
var speakerEQ = []byte{
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	127, 247, 0, 0, 128, 9, 0, 0, 127, 239, 0, 0, 0, 17, 0, 0,
	0, 17, 0, 0, 127, 222, 0, 0, 15, 0, 0,
}

var pathSequence = map[Output][]kctl{
	OutputSpeaker: {
		{name: "Audio_DacMux_Setting", value: "Off"},
		{name: "Right Channel Only", value: "On"},
		{name: driverGain, level: 6},
	},
	OutputHeadphone: {
		{name: "Ignore Ramp Up", value: "On"},
		{name: driverGain, level: 11},
		{name: "Audio_DacMux_Setting", value: "On"},
		{name: "Right Channel Only", value: "Off"},
	},
}

// The ext_headphone_output turnoff sequence.
var headphoneOff = []kctl{
	{name: "Audio_DacMux_Setting", value: "Off"},
	{name: "Right Channel Only", value: "On"},
	{name: "Ignore Ramp Up", value: "Off"},
}

const driverGain = "HP Driver Gain Volume"

func GainRange() (lo, hi int, ok bool) { return 0, 12, true }

// radar's codec adds three controls biscuit does not have.
const (
	// MFP2 gates the external amplifier: "On" holds it off, and that is how the driver leaves it.
	mfpMute = "MFP Gpio Mute"
	// The internal driver's own enable. Ext_Speaker_Amp_Switch is shared with the line-out here.
	speakerAmp = "Speaker_Amp_Switch"
	outputMux  = "Headphone_Speaker_Mux"
)

var radarClose = append(slices.Clone(initSequence), kctl{name: speakerAmp, value: "Off"})

var radarInit = append(slices.Clone(radarClose), kctl{name: mfpMute, value: "Off"})

// The kernel may raise MFP2 again on close, so every path lowers it.
var radarPath = map[Output][]kctl{
	OutputSpeaker: append([]kctl{
		{name: outputMux, value: "Speaker"},
		{name: mfpMute, value: "Off"},
		{name: AmpSwitch, value: "On"},
	}, pathSequence[OutputSpeaker]...),
	OutputHeadphone: append([]kctl{
		{name: outputMux, value: "Headphone"},
		{name: mfpMute, value: "Off"},
		{name: AmpSwitch, value: "On"},
	}, pathSequence[OutputHeadphone]...),
}

func sequencesFor(has func(control string) bool) sequences {
	if has != nil && has(mfpMute) {
		slog.Info("speaker amplifier", "gates", "radar")
		return sequences{
			init: radarInit,
			// Only the driver's enable goes off: the shared switch would take the line-out with it.
			on:    []kctl{{name: AmpSwitch, value: "On"}, {name: speakerAmp, value: "On"}},
			off:   []kctl{{name: speakerAmp, value: "Off"}},
			path:  radarPath,
			leave: map[Output][]kctl{OutputHeadphone: headphoneOff},
			close: radarClose,
			gain:  driverGain,
		}
	}
	return sequences{
		init:  initSequence,
		on:    []kctl{{name: AmpSwitch, value: "On"}},
		off:   []kctl{{name: AmpSwitch, value: "Off"}},
		path:  pathSequence,
		leave: map[Output][]kctl{OutputHeadphone: headphoneOff},
		close: initSequence,
		gain:  driverGain,
	}
}
