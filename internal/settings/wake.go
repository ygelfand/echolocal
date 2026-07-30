package settings

// Delivery is how a spoken reply reaches the device. It is per slot because a local pipeline and a
// cloud one differ in how long the audio takes to start, so the trade between starting sooner and not
// gapping is not the same for both.
type Delivery string

const (
	// DeliveryWhole fetches the reply from the url Home Assistant serves it at. It cannot gap and it
	// says when the audio has ended, which the stream does not.
	DeliveryWhole Delivery = "whole"

	// DeliveryStream takes the reply over the API as it is generated. It starts sooner and it can gap:
	// the chunks arrive at about the rate they play, so any hiccup empties the queue and splices
	// silence into the middle of a word.
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
