package settings

// Defaults for anything the user has never set. They live here with the values they belong to
// rather than at each call site, so a default cannot differ depending on who asked.
const (
	DefaultThreshold = 0.85
	DefaultEffect    = "Pulse"
	DefaultTone      = ToneChirp
	DefaultDelivery  = DeliveryWhole

	// Seconds. Nobody speaks for fifteen, and a pipeline with a model in it can take a while.
	DefaultMaxListen = 15
	DefaultMaxThink  = 90

	// Zero is no follow-up unless Home Assistant asks for one.
	DefaultFollowUp = 0

	// Milliseconds of a streamed reply to collect before playing it. Home Assistant paces itself to
	// stay this far ahead of a device it assumes plays everything on arrival, so it is what it expects
	// to be enough. A pipeline whose speech arrives slower than it plays needs more.
	DefaultBuffer = 384

	// Analog gain on the array in dB, where the vendor ran it.
	DefaultMicGain = 20

	DefaultMixing = MixCenter

	// Home Assistant no longer levels what a satellite sends, so the device does.
	DefaultLeveling = true

	// The ring does not follow the room until asked. A device that lights up whenever anyone speaks is
	// a choice, not a default: in a bedroom it is the opposite of what you want.
	DefaultReaction = ""

	// A failure says so, because a request that silently did nothing is the worst of the options.
	DefaultTrouble = "Alert"

	// A cut microphone does not, because the button has its own LED for exactly this and a ring held
	// lit for as long as someone leaves the device muted is both a light nobody asked for and, on this
	// hardware, an audible one.
	DefaultMuted = ""
)

// ReactionOr reports the animation that follows the room, TroubleOr what a failure shows and MutedOr
// what a cut microphone shows, each or def if it has never been set. Empty is a deliberate none, which
// is why these cannot be plain strings.
func (r Ring) ReactionOr(def string) string {
	if r.Reaction == nil {
		return def
	}
	return *r.Reaction
}

func (r Ring) TroubleOr(def string) string {
	if r.Trouble == nil {
		return def
	}
	return *r.Trouble
}

func (r Ring) MutedOr(def string) string {
	if r.Muted == nil {
		return def
	}
	return *r.Muted
}

// Stored reports whether the light's appearance was ever saved. Nothing saved is a ring that comes up
// dark rather than one that comes up guessing.
func (l Light) Stored() bool { return l.On != nil }

// OnOr and the rest read the saved appearance. Brightness and the channels are 0 to 1, as Home
// Assistant sends them.
func (l Light) OnOr(def bool) bool {
	if l.On == nil {
		return def
	}
	return *l.On
}

func (l Light) BrightnessOr(def float32) float32 { return floatOr(l.Brightness, def) }
func (l Light) RedOr(def float32) float32        { return floatOr(l.Red, def) }
func (l Light) GreenOr(def float32) float32      { return floatOr(l.Green, def) }
func (l Light) BlueOr(def float32) float32       { return floatOr(l.Blue, def) }

func (l Light) EffectOr(def string) string {
	if l.Effect == nil {
		return def
	}
	return *l.Effect
}

func floatOr(v *float32, def float32) float32 {
	if v == nil {
		return def
	}
	return *v
}

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

// LevelingOr reports whether the mix is brought up to the level recognition expects.
func (m Microphone) LevelingOr(def bool) bool {
	if m.Leveling == nil {
		return def
	}
	return *m.Leveling
}

// GainOr reports the analog gain on the array, in dB.
func (m Microphone) GainOr(def int) int {
	if m.Gain == nil {
		return def
	}
	return *m.Gain
}

// MixingOr reports how the array is combined.
func (m Microphone) MixingOr(def Mixing) Mixing {
	if m.Mixing == nil {
		return def
	}
	return *m.Mixing
}

// Slot is the configuration of one wake word slot. A slot that has never been set comes back empty,
// which means switched off.
func (w Wake) Slot(slot int) WakeWord {
	if slot < 0 || slot >= len(w.Words) {
		return WakeWord{}
	}
	return w.Words[slot]
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

// DeliveryOr reports how a reply reaches the device.
func (w WakeWord) DeliveryOr(def Delivery) Delivery {
	if w.Delivery == nil {
		return def
	}
	return *w.Delivery
}

// BufferOr reports how much of a streamed reply to collect before playing it, in milliseconds.
func (w WakeWord) BufferOr(def int) int {
	if w.Buffer == nil {
		return def
	}
	return *w.Buffer
}

// FollowUpOr reports how long a turn opened without a wake word listens for.
func (w WakeWord) FollowUpOr(def int) int {
	if w.FollowUp == nil {
		return def
	}
	return *w.FollowUp
}

// MaxListenOr and MaxThinkOr report the limits in seconds.
func (w WakeWord) MaxListenOr(def int) int {
	if w.MaxListen == nil {
		return def
	}
	return *w.MaxListen
}

func (w WakeWord) MaxThinkOr(def int) int {
	if w.MaxThink == nil {
		return def
	}
	return *w.MaxThink
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
