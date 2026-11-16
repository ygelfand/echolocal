//go:build board_athena

package mic

import "github.com/ygelfand/echolocal/internal/lib/subband"

const (
	Channels = 6
	Mics     = 4
)

const CenterMic = 0

// Four mics and no centre one: not the ring beam.go models.
const delaySum = false

// athena's folder also holds the dot's FBFV2 file name and crown's coefs_FBF.cfg.
var tunings = []subband.Tuning{subband.AthenaFBFV2}

const lowCutHz = 100
