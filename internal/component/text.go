package component

import "unicode/utf8"

// StateLimit is the longest state Home Assistant will store — MAX_LENGTH_STATE_STATE in
// homeassistant/const.py. Anything longer is refused and the entity reads unknown instead, so a long
// value loses the whole sensor rather than its tail. It counts characters, not bytes.
const StateLimit = 255

// Fit shortens text to what Home Assistant accepts.
func Fit(text string) string {
	if utf8.RuneCountInString(text) <= StateLimit {
		return text
	}

	kept := 0
	for i := range text {
		if kept == StateLimit-1 {
			return text[:i] + "…"
		}
		kept++
	}
	return text
}
