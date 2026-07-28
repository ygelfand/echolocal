package satellite

import (
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/speaker"
)

// The tones the buttons make. Direction carries the meaning — rising for on, falling for off — and
// a hold is two notes where a press is one, so they are told apart without looking.
const toneLevel = 0.3

var (
	toneVolume = []speaker.Note{{Freq: 880, Ms: 80}}

	toneMute     = []speaker.Note{{Freq: 880, Ms: 70}, {Freq: 587, Ms: 110}}
	toneUnmute   = []speaker.Note{{Freq: 587, Ms: 70}, {Freq: 880, Ms: 110}}
	toneMuteHold = []speaker.Note{{Freq: 587, Ms: 70}, {Freq: 440, Ms: 130}}

	// toneTrouble falls twice and ends low, which no acknowledgement does. A request that cannot be
	// served has to sound different from one that was, or a failure is indistinguishable from the
	// device having ignored the person entirely.
	toneTrouble = []speaker.Note{{Freq: 622, Ms: 90}, {Freq: 466, Ms: 90}, {Freq: 349, Ms: 180}}

	// toneCancel is one short falling pair: the request was dropped, which is neither a failure nor
	// an answer.
	toneCancel = []speaker.Note{{Freq: 698, Ms: 60}, {Freq: 466, Ms: 90}}
)

// wakeTones is what a detection can sound like. They are told apart by shape rather than pitch, so
// two wake words set to different ones are distinguishable without knowing which is which.
var wakeTones = map[settings.Tone][]speaker.Note{
	settings.ToneNone:  nil,
	settings.ToneChirp: {{Freq: 784, Ms: 60}, {Freq: 1175, Ms: 90}},
	settings.ToneDing:  {{Freq: 1319, Ms: 200}},
	settings.ToneRise:  {{Freq: 659, Ms: 55}, {Freq: 880, Ms: 55}, {Freq: 1319, Ms: 110}},
}

// WakeTones lists them in the order they are offered.
func WakeTones() []settings.Tone {
	return []settings.Tone{settings.ToneNone, settings.ToneChirp, settings.ToneDing, settings.ToneRise}
}

func chime(spk *speaker.Player, notes []speaker.Note) {
	if spk == nil || len(notes) == 0 {
		return
	}
	spk.Chime(toneLevel, notes...)
}
