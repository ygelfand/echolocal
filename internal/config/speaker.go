package config

type Speaker struct {
	Volume     int        `json:"volume"`
	Resampling Resampling `json:"resampling"`
	ASP        bool       `json:"asp"`

	SpeakerOutput   OutputMode `json:"speaker_output"`
	HeadphoneOutput OutputMode `json:"headphone_output"`

	SpeakerGain int `json:"speaker_gain"`
}

const (
	// Android's STREAM_MUSIC and the vendor volume curves run 0..30.
	VolumeSteps = 30

	DefaultVolume = VolumeSteps / 2

	DefaultResampling = ResampleSinc

	DefaultASP = true

	DefaultSpeakerGain = 6
)

func defaultSpeaker() Speaker {
	return Speaker{
		Volume:          DefaultVolume,
		Resampling:      DefaultResampling,
		ASP:             DefaultASP,
		SpeakerOutput:   OutputAuto,
		HeadphoneOutput: OutputAuto,
		SpeakerGain:     DefaultSpeakerGain,
	}
}

type SpeakerWriter struct{ st *Store }

func (w SpeakerWriter) Volume(v int) error {
	return w.st.Update(func(c *Config) { c.Speaker.Volume = v })
}

func (w SpeakerWriter) Resampling(v Resampling) error {
	return w.st.Update(func(c *Config) { c.Speaker.Resampling = v })
}

func (w SpeakerWriter) ASP(v bool) error {
	return w.st.Update(func(c *Config) { c.Speaker.ASP = v })
}

type OutputMode string

const (
	OutputAuto OutputMode = "auto"
	OutputOn   OutputMode = "on"
	OutputOff  OutputMode = "off"
)

func OutputModes() []OutputMode { return []OutputMode{OutputAuto, OutputOn, OutputOff} }

func (m OutputMode) Label() string {
	switch m {
	case OutputAuto:
		return "Auto"
	case OutputOn:
		return "On"
	case OutputOff:
		return "Off"
	}
	return string(m)
}

func (m OutputMode) Plays(auto bool) bool {
	switch m {
	case OutputOn:
		return true
	case OutputOff:
		return false
	}
	return auto
}

type Resampling string

const (
	ResampleSinc Resampling = "sinc"

	ResampleLinear Resampling = "linear"

	ResampleHold Resampling = "hold"
)

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
