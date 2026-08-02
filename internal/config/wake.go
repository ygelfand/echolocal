package config

// Wake is the wake word configuration, indexed by Home Assistant's wake word slot.
type Wake struct {
	Words []WakeWord `json:"words"`
}

// WakeWord is one slot: which wake word listens there and how it behaves when it fires. An empty ID
// is the slot switched off, which is also how detection is turned off altogether.
type WakeWord struct {
	ID string `json:"id"`

	// Threshold is the score a detection has to reach. Per word, because models disagree on scale.
	Threshold float64 `json:"threshold"`

	Tone   Tone   `json:"tone"`
	Effect string `json:"effect"`

	// Delivery is how the reply from this slot's pipeline reaches the device.
	Delivery Delivery `json:"delivery"`

	// FollowUp is seconds to listen after a reply, zero to only do it when Home Assistant asks.
	FollowUp int `json:"follow_up"`

	// Buffer is milliseconds of a streamed reply to collect before playing any of it.
	Buffer int `json:"buffer"`

	// Seconds before giving up. Listening holds the microphone open and Home Assistant normally ends
	// it, so that one is a backstop; thinking holds only the ring, and a model can take a minute.
	MaxListen int `json:"max_listen"`
	MaxThink  int `json:"max_think"`
}

const (
	DefaultThreshold = 0.85
	DefaultEffect    = "Pulse"
	DefaultTone      = ToneChirp
	DefaultDelivery  = DeliveryWhole

	DefaultMaxListen = 15
	DefaultMaxThink  = 90

	// Zero is no follow-up unless Home Assistant asks for one.
	DefaultFollowUp = 0

	// Home Assistant paces itself to stay 384 ms ahead, so holding that much consumes the whole lead:
	// measured, 384 gave 8 seams in a 13 second reply and 650 gave one.
	DefaultBuffer = 650
)

// DefaultWakeWord is a slot nobody has set: switched off, and everything else ready for when it is.
func DefaultWakeWord() WakeWord {
	return WakeWord{
		Threshold: DefaultThreshold,
		Tone:      DefaultTone,
		Effect:    DefaultEffect,
		Delivery:  DefaultDelivery,
		FollowUp:  DefaultFollowUp,
		Buffer:    DefaultBuffer,
		MaxListen: DefaultMaxListen,
		MaxThink:  DefaultMaxThink,
	}
}

// Slot is one wake word slot, or an unset one with the defaults in it.
func (w Wake) Slot(n int) WakeWord {
	if n < 0 || n >= len(w.Words) {
		return DefaultWakeWord()
	}
	return w.Words[n]
}

// Slots is the first n slots, one entry each whether or not any has been set.
func (w Wake) Slots(n int) []WakeWord {
	out := make([]WakeWord, n)
	for i := range out {
		out[i] = w.Slot(i)
	}
	return out
}

type WakeWriter struct {
	st   *Store
	slot int
}

func (w WakeWriter) ID(v string) error {
	return w.word(func(word *WakeWord) { word.ID = v })
}

func (w WakeWriter) Threshold(v float64) error {
	return w.word(func(word *WakeWord) { word.Threshold = v })
}

func (w WakeWriter) Tone(v Tone) error {
	return w.word(func(word *WakeWord) { word.Tone = v })
}

func (w WakeWriter) Effect(v string) error {
	return w.word(func(word *WakeWord) { word.Effect = v })
}

func (w WakeWriter) Delivery(v Delivery) error {
	return w.word(func(word *WakeWord) { word.Delivery = v })
}

func (w WakeWriter) FollowUp(seconds int) error {
	return w.word(func(word *WakeWord) { word.FollowUp = seconds })
}

func (w WakeWriter) Buffer(ms int) error {
	return w.word(func(word *WakeWord) { word.Buffer = ms })
}

func (w WakeWriter) MaxListen(seconds int) error {
	return w.word(func(word *WakeWord) { word.MaxListen = seconds })
}

func (w WakeWriter) MaxThink(seconds int) error {
	return w.word(func(word *WakeWord) { word.MaxThink = seconds })
}

// word grows the list to reach the slot, so slot 1 can be set on a device where slot 0 never was.
// The slots invented along the way get the defaults rather than zeros.
func (w WakeWriter) word(f func(*WakeWord)) error {
	if w.slot < 0 {
		return errSlot(w.slot)
	}
	return w.st.Update(func(c *Config) {
		for len(c.Wake.Words) <= w.slot {
			c.Wake.Words = append(c.Wake.Words, DefaultWakeWord())
		}
		f(&c.Wake.Words[w.slot])
	})
}

// Delivery is how a spoken reply reaches the device. It is per slot because a local pipeline and a
// cloud one differ in how long the audio takes to start, so the trade between starting sooner and
// not gapping is not the same for both.
type Delivery string

const (
	// DeliveryWhole fetches the reply from the url Home Assistant serves it at. It cannot gap and it
	// says when the audio has ended, which the stream does not.
	DeliveryWhole Delivery = "whole"

	// DeliveryStream takes the reply over the API as it is generated. It starts sooner and it can gap:
	// the chunks arrive at about the rate they play, so any hiccup splices silence into a word.
	DeliveryStream Delivery = "stream"
)

// Label is how the setting is shown.
func (d Delivery) Label() string {
	switch d {
	case DeliveryWhole:
		return "Whole file"
	case DeliveryStream:
		return "Streamed"
	}
	return string(d)
}

// Tone is the sound a wake word makes when it fires.
type Tone string

const (
	// ToneNone is silence: the ring is feedback enough for some people.
	ToneNone Tone = "none"

	// ToneChirp is two quick rising notes.
	ToneChirp Tone = "chirp"

	// ToneDing is one longer note, for somewhere noisy.
	ToneDing Tone = "ding"

	// ToneRise is three ascending notes, the most conspicuous of them.
	ToneRise Tone = "rise"
)

// Label is how the setting is shown.
func (t Tone) Label() string {
	switch t {
	case ToneNone:
		return "None"
	case ToneChirp:
		return "Chirp"
	case ToneDing:
		return "Ding"
	case ToneRise:
		return "Rise"
	}
	return string(t)
}
