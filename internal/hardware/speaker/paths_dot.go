//go:build !board_checkers && !board_cronos && !board_crown && !board_athena

package speaker

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

func sequencesFor(func(control string) bool) sequences {
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
