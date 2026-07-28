package settings

// Resampling is how the 16 kHz voice a pipeline sends is stretched to the 48 kHz the codec takes.
type Resampling string

const (
	// ResampleSinc interpolates through a low pass at the input's Nyquist, which is the correct
	// answer and what the device uses unless told otherwise.
	ResampleSinc Resampling = "sinc"

	// ResampleLinear draws a straight line between input samples. It attenuates the images rather
	// than removing them, for a fraction of the work.
	ResampleLinear Resampling = "linear"

	// ResampleHold repeats each input sample, which is what the device did before any of this and
	// leaves the images in full.
	ResampleHold Resampling = "hold"
)

// Label is how the setting is shown.
func (r Resampling) Label() string {
	switch r {
	case ResampleSinc:
		return "Band limited"
	case ResampleLinear:
		return "Linear"
	case ResampleHold:
		return "Repeat samples"
	}
	return string(r)
}
