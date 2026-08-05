package component

import "unicode/utf8"

// StateLimit is the longest state Home Assistant will store — MAX_LENGTH_STATE_STATE in
// homeassistant/const.py. Anything longer is refused and the entity reads unknown instead, so a long
// value loses the whole sensor rather than its tail. It counts characters, not bytes.
const StateLimit = 255

// EventLimit is the longest text an event carries. Event data has no limit of its own; the transport
// does, at 65515 bytes for the whole message.
const EventLimit = 8000

// Fit shortens text to what Home Assistant will store as a state.
func Fit(text string) string { return clip(text, StateLimit) }

// FitEvent shortens text to what an event can carry, which is far more than a state.
func FitEvent(text string) string { return clip(text, EventLimit) }

func clip(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}

	kept := 0
	for i := range text {
		if kept == limit-1 {
			return text[:i] + "…"
		}
		kept++
	}
	return text
}
