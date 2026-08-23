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

	// Cancel subtracts what the speaker is playing from what the microphones hear, so a wake word
	// during a reply competes with the room rather than with the reply.
	Cancel bool `json:"cancel"`

	// Sensitivity is how far over the room's own floor, in dB, counts as something happening. Lower
	// notices a chair being moved; higher waits for someone to speak.
	Sensitivity int `json:"sensitivity"`

	// Denoise estimates the steady part of the room and takes it out of what the microphones heard.
	Denoise bool `json:"denoise"`
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

	DefaultCancel = true

	// Measured on a quiet room: 0.8 dB at the 99th percentile of frames, so this is well clear of the
	// room itself and is really about brief small sounds — a chair, a keyboard.
	DefaultSensitivity = 8

	DefaultDenoise = false
)

func defaultMicrophone() Microphone {
	return Microphone{
		Muted:       DefaultMuted,
		LEDBright:   DefaultLEDBright,
		Gain:        DefaultMicGain,
		Leveling:    DefaultLeveling,
		Mixing:      DefaultMixing,
		Cancel:      DefaultCancel,
		Sensitivity: DefaultSensitivity,
		Denoise:     DefaultDenoise,
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

func (w MicrophoneWriter) Cancel(v bool) error {
	return w.st.Update(func(c *Config) { c.Microphone.Cancel = v })
}

func (w MicrophoneWriter) Sensitivity(db int) error {
	return w.st.Update(func(c *Config) { c.Microphone.Sensitivity = db })
}

func (w MicrophoneWriter) Denoise(v bool) error {
	return w.st.Update(func(c *Config) { c.Microphone.Denoise = v })
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
