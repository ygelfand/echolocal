package satellite

import (
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
	a.heard.Set(text)
}

func (a *activity) Replied(text string) {
	if a == nil || text == "" {
		return
	}
	a.reply.Set(text)
}
