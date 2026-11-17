package subband

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Tuning names one of the vendor front ends Load knows.
//
// Amazon reuses coefficient file names across arrays that differ: athena ships
// coefs_FBFV2_LowLatency_8beams.cfg for its own 4 mics, the dot for 4 of its 7. A file name alone
// cannot say which array it was tuned for, so each board names the tunings its firmware may carry.
type Tuning string

const (
	FBF           Tuning = "FilterBank_640 + FBF"
	FBFV2         Tuning = "FilterBank_768cvxGLow + FBFV2 8 beams"
	RadarFBFV2    Tuning = "FilterBank_768cvxGLow + FBFV2 4micsSq 8 beams"
	AthenaFBFV2   Tuning = "FilterBank_768cvxGLow + FBFV2 athena 4 mics 8 beams"
	CheckersFBFV2 Tuning = "FilterBank_768cvxGLow + FBFV2 checkers 2 mics 7 beams"
)

// DotTunings are the front ends the dot's firmwares carry. radar shares the dot's build.
var DotTunings = []Tuning{FBF, FBFV2, RadarFBFV2}

type geometry struct {
	name Tuning

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
		name:        FBF,
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
	athenaFBFV2(),
	checkersFBFV2(),
}

var fbfV2 = geometry{
	// AFE.cfg "ASR FixedBeamFormerV2" Channel Map [1,2,4,5]: "This selects 4 mics out of 7".
	name:      FBFV2,
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
	g.name = RadarFBFV2
	g.mics = []int{0, 1, 3, 4}
	g.boostDB = 2.51
	g.weightsFile = "coefs_FBFV2_4micsSq_8beams_idnr_m15_lowLatency.cfg"
	return g
}

// athena's AFE.cfg: the same filter bank, FixedBeamFormerV2 (8 beams, 4 taps) and 3-beam group
// merger as fbfV2, under fbfV2's weights file name. Its FixedBeamFormerV2 has no Channel Map: the
// array is its 4 mics, captured as channels 0-3. Mics Post AEC Output Gain BoostdB 2.2.
func athenaFBFV2() geometry {
	g := fbfV2
	g.name = AthenaFBFV2
	g.mics = []int{0, 1, 2, 3}
	g.boostDB = 2.2
	return g
}

// checkers' AFE.cfg: the same filter bank as fbfV2, FixedBeamFormerV2 with 7 beams over its 2 mics
// (channels 0 and 1, no Channel Map) in its own weights file, laid out as fbfV2's. Its
// GroupBeamMergerV2 3beamsGroup lists 7 groups that do not wrap round, as a pair of mics covers a
// half circle: [[0,1,1],[0,1,2],[1,2,3],[2,3,4],[3,4,5],[4,5,6],[5,5,6]], margin 1.26, hold 12.
// Mics Post AEC Output Gain BoostdB 4.3.
func checkersFBFV2() geometry {
	g := fbfV2
	g.name = CheckersFBFV2
	g.Beams = 7
	g.mics = []int{0, 1}
	g.groups = [][]int{{0, 1, 1}, {0, 1, 2}, {1, 2, 3}, {2, 3, 4}, {3, 4, 5}, {4, 5, 6}, {5, 5, 6}}
	g.boostDB = 4.3
	g.weightsFile = "coefs_FBFV2_LowLatency_7beams.cfg"
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
func (w *Weights) Name() string { return string(w.g.name) }
func (w *Weights) Inputs() int  { return w.g.Inputs() }

// Load reads the first of the allowed tunings, in the order known lists them, whose weights file
// is in dir. A board allows only the tunings its own array can carry; see Tuning.
func Load(dir string, allowed []Tuning) (*Weights, error) {
	if len(allowed) == 0 {
		return nil, fmt.Errorf("no tuning allowed for %s: this board names none", dir)
	}
	for _, name := range allowed {
		if !slices.ContainsFunc(known, func(g geometry) bool { return g.name == name }) {
			return nil, fmt.Errorf("%q is not a tuning we know", name)
		}
	}
	for _, g := range known {
		if !slices.Contains(allowed, g.name) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, g.weightsFile)); err != nil {
			continue
		}
		return g.load(dir)
	}
	return nil, fmt.Errorf("%s holds none of the coefficient sets for %v", dir, allowed)
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
