//go:build !board_checkers && !board_cronos && !board_crown && !board_athena

package mic

import "github.com/ygelfand/echolocal/internal/lib/subband"

// The capture codec accepts one format only: 16 kHz, S24_3LE, 9 channels.
const (
	Channels      = 9
	CaptureDevice = 24
)

// ch7 and ch8 are the playback loopback.
const Mics = 7

const CenterMic = 6

// adcs are the four converters the seven microphones arrive on.
var adcs = []string{"A", "B", "C", "D"}

// beam.go models this ring: six mics 36 mm out and one in the centre.
const delaySum = true

// The dot firmwares carry one of these. radar shares this build, so all three are allowed.
var tunings = []subband.Tuning{subband.FBF, subband.FBFV2, subband.RadarFBFV2}

const lowCutHz = 0
