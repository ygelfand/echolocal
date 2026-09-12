// Package subband is the beamformer the device shipped with: a DFT-modulated filter bank feeding a
// fixed filter-and-sum beamformer, both driven by the vendor's own coefficients.
//
// It is not a better delay-and-sum. Delay-and-sum aligns whole microphones by a fractional sample
// and averages them, which is one gain and one delay per microphone. This has a complex weight per
// band, beam, tap and microphone — thousands of them, generated for this enclosure — so it can shape
// its response differently at 300 Hz than at 3 kHz, which an array 36 mm across needs to do.
//
// The coefficients are on every device we install to and are not ours to ship, so they are read
// from the vendor partition at runtime and this whole mixing option is absent when they are not
// there.
package subband

import (
	"fmt"
	"os"
	"path/filepath"
)

// geometry is the shape of one vendor front end. Fire OS 5 and Fire OS 6 ship different ones, and a
// file belongs to exactly one: reading either with the other's dimensions would point the beams at
// nothing, so nothing here is shared between them.
type geometry struct {
	name string

	// Bands is how many subbands carry the signal. The bank is real-input, so these are the lower
	// half of the transform.
	Bands int

	// FFTLen is the transform the fold feeds, and Hop how many new samples each frame takes. Two
	// samples of transform per sample of hop makes the bank twice oversampled, which is what keeps
	// aliasing low enough to filter inside a band.
	FFTLen int
	Hop    int

	// WindowLen is the prototype filter.
	WindowLen int

	// Beams over the circle, and Taps how deep each band's filter is.
	Beams int
	Taps  int

	// How the coefficient file is laid out: order names the four axes outermost first, perGroup how
	// many bands share a block, split whether a block's real parts come before its imaginary ones
	// rather than interleaved, and tapDown whether taps count backwards.
	order    axes
	perGroup int
	split    bool
	tapDown  bool

	// mics are the array channels the weights were generated for, in the order the file holds them.
	// Fire OS 6 beamforms over four of the seven.
	mics []int

	// boostDB is the gain the vendor applies after the beamformer. The weights are not normalised,
	// and without it the mix lands well below the center mic.
	boostDB float64

	windowFile  string
	weightsFile string
}

// The two front ends, from each firmware's own AFE.cfg.
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
		boostDB:     7.2,
		windowFile:  "coefs_FilterBank_640.cfg",
		weightsFile: "coefs_FBF.cfg",
	},
	{
		// "ASR FilterBank" and "ASR FixedBeamFormerV2", whose Channel Map is [1,2,4,5] with the
		// comment "This selects 4 mics out of 7". LowLatency is what it names for both the normal and
		// the diffused noise case, with EVD disabled.
		name:      "FilterBank_768cvxGLow + FBFV2 8 beams",
		Bands:     128,
		FFTLen:    256,
		Hop:       128,
		WindowLen: 768,
		Beams:     8,
		Taps:      4,
		order:     "maet",
		perGroup:  1,
		// Four ring positions, and only their rotation about the ring is fixed: rotating the map
		// rotates every beam with it, which is how this ordering was recognised as the right one.
		// AFE.cfg's "Channel Map": [1,2,4,5] counts channels in Amazon's order, not this array's.
		mics:        []int{0, 3, 2, 1},
		boostDB:     7.2,
		windowFile:  "coefs_FilterBank_AnalysisSynthesis_768cvxGLow.cfg",
		weightsFile: "coefs_FBFV2_LowLatency_8beams.cfg",
	},
}

// Inputs is how many microphones the weights combine.
func (g geometry) Inputs() int { return len(g.mics) }

// Channels is how many the array has to deliver for the selection above to be satisfiable.
func (g geometry) Channels() int {
	most := 0
	for _, m := range g.mics {
		most = max(most, m)
	}
	return most + 1
}

// The beam is chosen on roughly 250 Hz to 4 kHz. Below that the array has no directivity to speak of
// and the room has most of its energy; above it, little of speech is left. The bank spans 8 kHz
// whatever its width, so the edges follow the band count.
func (g geometry) firstSpeechBand() int { return g.Bands / 32 }
func (g geometry) lastSpeechBand() int  { return g.Bands / 2 }

// holdFrames is how long a direction has to keep winning before the beam switches — about 160 ms, so
// a door closing off to one side does not swing the beam mid-word. A frame is Hop samples at 16 kHz.
func (g geometry) holdFrames() int { return 160 * 16000 / 1000 / g.Hop }

// VendorDir is where the vendor keeps its tuning.
const VendorDir = "/vendor/etc/audio-algorithms"

// Weights is everything parsed out of the vendor's files, shared by every stream that uses it.
type Weights struct {
	g geometry

	// window is the filter bank's prototype.
	window []float32

	// fbf is the beamformer, indexed by weight below.
	fbf []complex64
}

// weight is the microphone weights for one band, beam and tap.
func (w *Weights) weight(band, beam, tap int) []complex64 {
	n := w.g.Inputs()
	at := ((band*w.g.Beams+beam)*w.g.Taps + tap) * n
	return w.fbf[at : at+n]
}

// Bands and Beams describe what was loaded, for whoever wants to say so.
func (w *Weights) Bands() int   { return w.g.Bands }
func (w *Weights) Beams() int   { return w.g.Beams }
func (w *Weights) Name() string { return w.g.name }
func (w *Weights) Inputs() int  { return w.g.Inputs() }

// Load reads the coefficients out of a directory, normally VendorDir. Which front end a device has is
// decided by which weights are there.
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
