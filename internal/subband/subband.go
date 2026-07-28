// Package subband is the beamformer the device shipped with: a DFT-modulated filter bank feeding a
// fixed filter-and-sum beamformer, both driven by the vendor's own coefficients.
//
// It is not a better delay-and-sum. Delay-and-sum aligns whole microphones by a fractional sample
// and averages them, which is one gain and one delay per microphone. This has a complex weight per
// band, beam, tap and microphone — 10752 of them, generated for this enclosure — so it can shape
// its response differently at 300 Hz than at 3 kHz, which an array 36 mm across needs to do.
//
// The coefficients are on every device we install to and are not ours to ship, so they are read
// from the vendor partition at runtime and this whole mixing option is absent when they are not
// there.
package subband

import "path/filepath"

// The filter bank the vendor's ASR path runs, from the configuration it logs at startup:
//
//	ConfDFTFilterBank  fftLen=128  deciRate=64  filterLen=640  bands=64
const (
	// Bands is how many subbands carry the signal. The bank is real-input, so these are the lower
	// half of the transform.
	Bands = 64

	// FFTLen is the transform the fold feeds, and Hop how many new samples each frame takes. Two
	// samples of transform per sample of hop makes the bank twice oversampled, which is what keeps
	// aliasing low enough to filter inside a band.
	FFTLen = 128
	Hop    = 64

	// WindowLen is the prototype filter, five transforms long.
	WindowLen = 5 * FFTLen

	// Beams over the circle, one per perimeter microphone, and Taps how deep each band's filter is.
	Beams = 6
	Taps  = 4

	// Inputs the weights were generated for, which is this array's seven microphones. It is the shape
	// of the coefficient file rather than a fact about the hardware, so it lives here.
	Inputs = 7
)

// VendorDir is where the vendor keeps its tuning.
const VendorDir = "/vendor/etc/audio-algorithms"

// The coefficient files. The window is titled AnalysisSynthesis, and is used for both.
const (
	windowFile  = "coefs_FilterBank_640.cfg"
	weightsFile = "coefs_FBF.cfg"
)

// Weights is everything parsed out of the vendor's files, shared by every stream that uses it.
type Weights struct {
	// window is the filter bank's prototype.
	window []float32

	// fbf is indexed [band][beam][tap][mic].
	fbf [Bands][Beams][Taps][Inputs]complex64
}

// Load reads the coefficients out of a directory, normally VendorDir.
func Load(dir string) (*Weights, error) {
	window, err := readFloats(filepath.Join(dir, windowFile), WindowLen)
	if err != nil {
		return nil, err
	}
	w := &Weights{window: window}
	if err := w.readFBF(filepath.Join(dir, weightsFile)); err != nil {
		return nil, err
	}
	return w, nil
}

