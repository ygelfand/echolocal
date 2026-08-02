package config

// Microphone is the array: whether it is cut, how it is combined, and how hard it is driven.
type Microphone struct {
	Muted     bool `json:"muted"`
	LEDBright bool `json:"led_bright"`

	// Gain is the analog gain on the array's converters, in dB.
	Gain int `json:"gain"`

	// Leveling brings the mix up to the level recognition expects.
	Leveling bool `json:"leveling"`

	// Mixing is how the seven microphones are combined. Which one wins depends on the room.
	Mixing Mixing `json:"mixing"`
}

const (
	// The microphones come up live, with their LED lit. A device that came up cut, or came up cut with
	// nothing saying so, is worse than either.
	DefaultMuted     = false
	DefaultLEDBright = true

	// Analog gain on the array in dB, where the vendor ran it.
	DefaultMicGain = 20

	DefaultMixing = MixCenter

	// Home Assistant no longer levels what a satellite sends, so the device does.
	DefaultLeveling = true
)

func defaultMicrophone() Microphone {
	return Microphone{
		Muted:     DefaultMuted,
		LEDBright: DefaultLEDBright,
		Gain:      DefaultMicGain,
		Leveling:  DefaultLeveling,
		Mixing:    DefaultMixing,
	}
}

type MicrophoneWriter struct{ st *Store }

func (w MicrophoneWriter) Muted(v bool) error {
	return w.st.Update(func(c *Config) { c.Microphone.Muted = v })
}

func (w MicrophoneWriter) LEDBright(v bool) error {
	return w.st.Update(func(c *Config) { c.Microphone.LEDBright = v })
}

func (w MicrophoneWriter) Gain(db int) error {
	return w.st.Update(func(c *Config) { c.Microphone.Gain = db })
}

func (w MicrophoneWriter) Leveling(v bool) error {
	return w.st.Update(func(c *Config) { c.Microphone.Leveling = v })
}

func (w MicrophoneWriter) Mixing(v Mixing) error {
	return w.st.Update(func(c *Config) { c.Microphone.Mixing = v })
}

// Mixing is how a microphone array is reduced to the single channel recognition reads.
type Mixing string

const (
	// MixCenter is the middle microphone alone: no arrival delay, nothing computed, and the baseline
	// anything else has to beat.
	MixCenter Mixing = "center"

	// MixDelaySum aligns all seven microphones to a steered direction and averages them.
	MixDelaySum Mixing = "delay-sum"

	// MixBeamformer is the fixed beamformer the device shipped with, whose per-band coefficients are
	// on the device and were tuned on this enclosure.
	MixBeamformer Mixing = "beamformer"
)

// Label is how the setting is shown.
func (m Mixing) Label() string {
	switch m {
	case MixCenter:
		return "Center mic"
	case MixDelaySum:
		return "Delay and sum"
	case MixBeamformer:
		return "Beamformer"
	}
	return string(m)
}
