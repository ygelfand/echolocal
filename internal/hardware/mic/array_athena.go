//go:build board_athena

package mic

import "github.com/ygelfand/echolocal/internal/lib/subband"

const (
	Channels = 6
	Mics     = 4
)

const CenterMic = 0

const beamforming = false

var tunings []subband.Tuning

const lowCutHz = 100
