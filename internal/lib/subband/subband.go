package subband

import (
	"fmt"
	"os"
	"path/filepath"
)

type geometry struct {
	name string

	Bands int

	FFTLen int
	Hop    int

	WindowLen int

	Beams int
	Taps  int

	order    axes
	perGroup int
	split    bool
	tapDown  bool

	mics []int

	groups [][]int
	margin float32
	hold   int

	// The vendor applies this gain after the beamformer; its weights are not normalised.
	boostDB float64

	windowFile  string
	weightsFile string
}

// The front ends, from each firmware's own AFE.cfg.
var known = []geometry{
	{
		name:        "FilterBank_640 + FBF",
		Bands:       64,
		FFTLen:      128,
		Hop:         64,
		WindowLen:   640,
		Beams:       6,
		Taps:        4,
		order:       "aetm",
		perGroup:    4,
		split:       true,
		tapDown:     true,
		mics:        []int{0, 1, 2, 3, 4, 5, 6},
		groups:      [][]int{{0}, {1}, {2}, {3}, {4}, {5}},
		margin:      1,
		hold:        40,
		boostDB:     7.2,
		windowFile:  "coefs_FilterBank_640.cfg",
		weightsFile: "coefs_FBF.cfg",
	},
	fbfV2,
	radarFBFV2(),
}

var fbfV2 = geometry{
	// AFE.cfg "ASR FixedBeamFormerV2" Channel Map [1,2,4,5]: "This selects 4 mics out of 7".
	name:      "FilterBank_768cvxGLow + FBFV2 8 beams",
	Bands:     128,
	FFTLen:    256,
	Hop:       128,
	WindowLen: 768,
	Beams:     8,
	Taps:      4,
	// The file zeroes band 0 once every 1024 values: beams and microphones outside, bands, then taps.
	order:    "emat",
	perGroup: 1,
	mics:     []int{1, 2, 4, 5},
	// AFE.cfg "ASR GroupBeamMergerV2 3beamsGroup": Beam Group Margin 1.26, Num Frames To Hold Beam Group 12.
	groups: [][]int{
		{0, 1, 2},
		{1, 2, 3},
		{2, 3, 4},
		{3, 4, 5},
		{4, 5, 6},
		{5, 6, 7},
		{6, 7, 0},
		{7, 0, 1},
	},
	margin:      1.26,
	hold:        12,
	boostDB:     7.2,
	windowFile:  "coefs_FilterBank_AnalysisSynthesis_768cvxGLow.cfg",
	weightsFile: "coefs_FBFV2_LowLatency_8beams.cfg",
}

// radar's AFE.cfg: FixedBeamFormerV2 Channel Map [0,1,3,4], Mics Post AEC Output Gain BoostdB 2.51.
func radarFBFV2() geometry {
	g := fbfV2
	g.name = "FilterBank_768cvxGLow + FBFV2 4micsSq 8 beams"
	g.mics = []int{0, 1, 3, 4}
	g.boostDB = 2.51
	g.weightsFile = "coefs_FBFV2_4micsSq_8beams_idnr_m15_lowLatency.cfg"
	return g
}

func (g geometry) Inputs() int { return len(g.mics) }

func (g geometry) Channels() int {
	most := 0
	for _, m := range g.mics {
		most = max(most, m)
	}
	return most + 1
}

func (g geometry) firstSpeechBand() int { return g.Bands / 32 }
func (g geometry) lastSpeechBand() int  { return g.Bands / 2 }

// AFE.cfg ARA_V2: Signal Power alpha fast 0.75; T frames GBM SNR switch 512, the noise buffer the minimum is taken over.
const (
	signalAlpha = 0.75
	noiseFrames = 512
)

const VendorDir = "/vendor/etc/audio-algorithms"

type Weights struct {
	g geometry

	window []float32

	fbf []complex64
}

func (w *Weights) weight(band, beam, tap int) []complex64 {
	n := w.g.Inputs()
	at := ((band*w.g.Beams+beam)*w.g.Taps + tap) * n
	return w.fbf[at : at+n]
}

func (w *Weights) Bands() int   { return w.g.Bands }
func (w *Weights) Beams() int   { return w.g.Beams }
func (w *Weights) Name() string { return w.g.name }
func (w *Weights) Inputs() int  { return w.g.Inputs() }

func Load(dir string) (*Weights, error) {
	for _, g := range known {
		if _, err := os.Stat(filepath.Join(dir, g.weightsFile)); err != nil {
			continue
		}
		return g.load(dir)
	}
	return nil, fmt.Errorf("%s holds none of the coefficient sets we know", dir)
}

func (g geometry) load(dir string) (*Weights, error) {
	window, err := readFloats(filepath.Join(dir, g.windowFile), g.WindowLen)
	if err != nil {
		return nil, err
	}
	w := &Weights{g: g, window: window}
	if err := w.readFBF(filepath.Join(dir, g.weightsFile)); err != nil {
		return nil, err
	}
	return w, nil
}
