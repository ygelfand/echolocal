package satellite

import (
	"unicode/utf8"

	esphome "github.com/ygelfand/go-esphome-device"
)

// activity is what a turn leaves behind in Home Assistant's logbook.
//
// The phases are already there: Home Assistant's own assist_satellite entity moves through idle,
// listening, processing and responding, and those state changes show in the feed. What it cannot show
// is content or which assistant answered, because it does not know either. These do.
type activity struct {
	word  *esphome.TextSensor
	heard *esphome.TextSensor
	reply *esphome.TextSensor
}

func newActivity() *activity {
	return &activity{
		word: &esphome.TextSensor{
			Base: esphome.Base{ObjectID: "last_wake_word", Name: "Last wake word", Icon: "mdi:account-voice"},
		},
		heard: &esphome.TextSensor{
			Base: esphome.Base{ObjectID: "last_heard", Name: "Last heard", Icon: "mdi:ear-hearing"},
		},
		reply: &esphome.TextSensor{
			Base: esphome.Base{ObjectID: "last_reply", Name: "Last reply", Icon: "mdi:message-text"},
		},
	}
}

func (a *activity) entities() []esphome.Entity {
	return []esphome.Entity{a.word, a.heard, a.reply}
}

// Woke records which wake word started a turn, so the transcript that follows can be attributed.
func (a *activity) Woke(phrase string) {
	if a == nil || phrase == "" {
		return
	}
	a.word.Set(phrase)
}

func (a *activity) Heard(text string) {
	if a == nil || text == "" {
		return
	}
	a.heard.Set(fit(text))
}

func (a *activity) Replied(text string) {
	if a == nil || text == "" {
		return
	}
	a.reply.Set(fit(text))
}

// stateLimit is the longest state Home Assistant will store — MAX_LENGTH_STATE_STATE in
// homeassistant/const.py. Anything longer is refused and the entity reads unknown instead, so a
// long reply loses the whole sensor rather than its tail. It counts characters, not bytes.
const stateLimit = 255

// fit shortens text to what Home Assistant accepts. The whole of it is in the log either way.
func fit(text string) string {
	if utf8.RuneCountInString(text) <= stateLimit {
		return text
	}

	kept := 0
	for i := range text {
		if kept == stateLimit-1 {
			return text[:i] + "…"
		}
		kept++
	}
	return text
}
