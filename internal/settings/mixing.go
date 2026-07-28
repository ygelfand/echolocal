package settings

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
