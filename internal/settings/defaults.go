package settings

// Defaults for anything the user has never set. They live here with the values they belong to
// rather than at each call site, so a default cannot differ depending on who asked.
const (
	DefaultThreshold = 0.85
	DefaultEffect    = "Pulse"
	DefaultTone      = ToneChirp
	DefaultBackend   = BackendOpenWakeWord
)

// VolumeOr reports the stored volume step, or def if it has never been set.
func (s Speaker) VolumeOr(def int) int {
	if s.Volume == nil {
		return def
	}
	return *s.Volume
}

// ResamplingOr reports how voice is stretched.
func (s Speaker) ResamplingOr(def Resampling) Resampling {
	if s.Resampling == nil {
		return def
	}
	return *s.Resampling
}

// MutedOr reports the stored mute state.
func (m Microphone) MutedOr(def bool) bool {
	if m.Muted == nil {
		return def
	}
	return *m.Muted
}

// LEDBrightOr reports the stored mute LED brightness.
func (m Microphone) LEDBrightOr(def bool) bool {
	if m.LEDBright == nil {
		return def
	}
	return *m.LEDBright
}

// MixingOr reports how the array is combined.
func (m Microphone) MixingOr(def Mixing) Mixing {
	if m.Mixing == nil {
		return def
	}
	return *m.Mixing
}

// BackendOr reports which engine runs the wake words.
func (w Wake) BackendOr(def WakeBackend) WakeBackend {
	if w.Backend == nil {
		return def
	}
	return *w.Backend
}

// Slot is the configuration of one wake word slot for the selected backend, filled in with defaults.
// A slot that has never been set comes back empty, which means switched off.
func (w Wake) Slot(slot int) WakeWord {
	words := w.Words[w.BackendOr(DefaultBackend)]
	if slot < 0 || slot >= len(words) {
		return WakeWord{}
	}
	return words[slot]
}

// WordID is the wake word listening in a slot, empty when the slot is off.
func (w Wake) WordID(slot int) string {
	if id := w.Slot(slot).ID; id != nil {
		return *id
	}
	return ""
}

// Words in the order Home Assistant's slots are numbered, up to n, so the caller gets one entry per
// slot whether or not it has ever been set.
func (w Wake) Slots(n int) []WakeWord {
	out := make([]WakeWord, n)
	for i := range out {
		out[i] = w.Slot(i)
	}
	return out
}

// ThresholdOr reports the score a detection has to reach.
func (w WakeWord) ThresholdOr(def float64) float64 {
	if w.Threshold == nil {
		return def
	}
	return *w.Threshold
}

// ToneOr reports what a detection sounds like.
func (w WakeWord) ToneOr(def Tone) Tone {
	if w.Tone == nil {
		return def
	}
	return *w.Tone
}

// EffectOr reports the ring animation a detection plays.
func (w WakeWord) EffectOr(def string) string {
	if w.Effect == nil {
		return def
	}
	return *w.Effect
}
