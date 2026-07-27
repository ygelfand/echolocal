package satellite

import "github.com/ygelfand/echolocal/internal/speaker"

// The tones the buttons make. Direction carries the meaning — rising for on, falling for off — and
// a hold is two notes where a press is one, so they are told apart without looking.
const toneLevel = 0.3

var (
	toneVolume = []speaker.Note{{Freq: 880, Ms: 80}}

	toneMute     = []speaker.Note{{Freq: 880, Ms: 70}, {Freq: 587, Ms: 110}}
	toneUnmute   = []speaker.Note{{Freq: 587, Ms: 70}, {Freq: 880, Ms: 110}}
	toneMuteHold = []speaker.Note{{Freq: 587, Ms: 70}, {Freq: 440, Ms: 130}}

	toneAction     = []speaker.Note{{Freq: 1046, Ms: 70}}
	toneActionHold = []speaker.Note{{Freq: 1046, Ms: 70}, {Freq: 1568, Ms: 130}}
)

func chime(spk *speaker.Player, notes []speaker.Note) {
	if spk == nil {
		return
	}
	spk.Chime(toneLevel, notes...)
}
