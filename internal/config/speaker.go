package config

// Speaker is how loud the device is and how voice is stretched to the playback rate.
type Speaker struct {
	Volume     int        `json:"volume"`
	Resampling Resampling `json:"resampling"`
}

const (
	// VolumeSteps runs 0..30, the range Android gives STREAM_MUSIC and the one the vendor's volume
	// curves are indexed by, so a step here is a step there. Home Assistant works in 0..1.
	VolumeSteps = 30

	// Half way up, so a device nobody has turned up is audible without being startling.
	DefaultVolume = VolumeSteps / 2

	DefaultResampling = ResampleSinc
)

func defaultSpeaker() Speaker {
	return Speaker{Volume: DefaultVolume, Resampling: DefaultResampling}
}

type SpeakerWriter struct{ st *Store }

func (w SpeakerWriter) Volume(v int) error {
	return w.st.Update(func(c *Config) { c.Speaker.Volume = v })
}

func (w SpeakerWriter) Resampling(v Resampling) error {
	return w.st.Update(func(c *Config) { c.Speaker.Resampling = v })
}

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
