package speaker

import (
	"math"
	"os"
	"strings"
	"time"
)

// Output is one of the device's audio outputs.
type Output string

const (
	OutputSpeaker   Output = "speaker"
	OutputHeadphone Output = "headphone"
)

// The mixer sequences below come direct from the device.
type kctl struct {
	name  string
	value string
	level int32
}

var initSequence = []kctl{
	{name: AmpSwitch, value: "Off"},
	{name: "Audio_DacMux_Setting", value: "On"},
	{name: "Ignore Ramp Up", value: "Off"},
	{name: driverGain, level: 0},
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

// headphoneOff is the ext_headphone_output turnoff sequence.
var headphoneOff = []kctl{
	{name: "Audio_DacMux_Setting", value: "Off"},
	{name: "Right Channel Only", value: "On"},
	{name: "Ignore Ramp Up", value: "Off"},
}

const driverGain = "HP Driver Gain Volume"

// jackState is the kernel's headphone jack switch: 1 while something is plugged in.
const jackState = "/sys/class/switch/h2w/state"

// jackPoll is how often the jack switch is sampled.
const jackPoll = 500 * time.Millisecond

// DetectOutput picks the output to use. A missing switch means no jack detection, so assume the
// speaker.
func DetectOutput() Output {
	b, err := os.ReadFile(jackState)
	if err != nil || strings.TrimSpace(string(b)) == "0" {
		return OutputSpeaker
	}
	return OutputHeadphone
}

// VolumeSteps is the number of volume steps, matching the vendor's curves.
const VolumeSteps = 30

// The vendor's volume curves, direct from the device: index is the volume step, value is
// attenuation in dB.
var volumeCurves = map[Output][VolumeSteps + 1]float64{
	OutputSpeaker: {
		-90, -33, -30, -26, -25, -23, -21, -19, -17, -16,
		-14, -13, -12, -10, -9, -8, -7, -5, -5, -4,
		-4, -4, -4, -3, -3, -3, -3, -3, -2, -1, 0,
	},
	OutputHeadphone: {
		-100, -39, -38, -36, -34, -33, -31, -29, -27, -26,
		-25, -24, -23, -21, -20, -19, -18, -17, -15, -14,
		-13, -12, -11, -9, -8, -7, -6, -4, -3, -2, 0,
	},
}

// mute is the attenuation the curves use for step 0.
const mute = -90

// gainForStep converts a volume step to a linear gain using the output's curve.
func gainForStep(out Output, step int) float32 {
	curve, ok := volumeCurves[out]
	if !ok {
		curve = volumeCurves[OutputSpeaker]
	}
	step = max(0, min(step, VolumeSteps))

	db := curve[step]
	if db <= mute {
		return 0
	}
	return float32(math.Pow(10, db/20))
}
