package speaker

import "github.com/ygelfand/echolocal/internal/config"

// The sounds the device makes about itself, as opposed to anything it was asked to play.
//
// Direction carries the meaning — rising for on, falling for off — and a hold is two notes where a
// press is one, so they are told apart without looking.
const toneLevel = 0.3

var (
	ToneVolume = []Note{{Freq: 880, Ms: 80}}

	ToneMute     = []Note{{Freq: 880, Ms: 70}, {Freq: 587, Ms: 110}}
	ToneUnmute   = []Note{{Freq: 587, Ms: 70}, {Freq: 880, Ms: 110}}
	ToneMuteHold = []Note{{Freq: 587, Ms: 70}, {Freq: 440, Ms: 130}}

	// ToneTrouble falls twice and ends low, which no acknowledgement does. A request that cannot be
	// served has to sound different from one that was, or a failure is indistinguishable from the
	// device having ignored the person entirely.
	ToneTrouble = []Note{{Freq: 622, Ms: 90}, {Freq: 466, Ms: 90}, {Freq: 349, Ms: 180}}

	// ToneCancel is one short falling pair: the request was dropped, which is neither a failure nor
	// an answer.
	ToneCancel = []Note{{Freq: 698, Ms: 60}, {Freq: 466, Ms: 90}}
)

// wakeTones is what a detection can sound like. They are told apart by shape rather than pitch, so
// two wake words set to different ones are distinguishable without knowing which is which.
var wakeTones = map[config.Tone][]Note{
	config.ToneNone:  nil,
	config.ToneChirp: {{Freq: 784, Ms: 60}, {Freq: 1175, Ms: 90}},
	config.ToneDing:  {{Freq: 1319, Ms: 200}},
	config.ToneRise:  {{Freq: 659, Ms: 55}, {Freq: 880, Ms: 55}, {Freq: 1319, Ms: 110}},
}

// WakeTone is what a slot set to t sounds like.
func WakeTone(t config.Tone) []Note { return wakeTones[t] }

// WakeTones lists them in the order they are offered.
func WakeTones() []config.Tone {
	return []config.Tone{config.ToneNone, config.ToneChirp, config.ToneDing, config.ToneRise}
}

// Chime plays a tone alongside whatever is playing rather than instead of it: pressing volume during
// a reply should beep and leave the reply alone.
func (d *Driver) Chime(notes []Note) {
	if d == nil || len(notes) == 0 {
		return
	}
	d.Interject(func(p *Player) { p.Chime(toneLevel, notes...) })
}
