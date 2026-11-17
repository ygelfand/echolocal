//go:build board_crown

package mic

import "github.com/ygelfand/echolocal/internal/lib/subband"

// Device 22 offers 16 kHz, S24_3LE, 6 channels; ch0-3 are the microphones and ch4/ch5 read silent.
const (
	Channels      = 6
	CaptureDevice = 22
)

const Mics = 4

const CenterMic = 0

var adcs = []string{"A", "B"}

const delaySum = false

// crown also ships a 4-mic coefs_FBF.cfg its AFE.cfg marks unused.
var tunings = []subband.Tuning{subband.CrownFBFV2}

const lowCutHz = 100
