package satellite

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFitKeepsWhatHomeAssistantAccepts(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"short", "the kitchen light is on"},
		{"empty", ""},
		{"exactly the limit", strings.Repeat("a", stateLimit)},
		{"exactly the limit in two byte runes", strings.Repeat("é", stateLimit)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fit(tc.text); got != tc.text {
				t.Errorf("shortened %d characters to %d", utf8.RuneCountInString(tc.text), utf8.RuneCountInString(got))
			}
		})
	}
}

// Home Assistant counts characters, so a limit applied to bytes would cut a reply with any accent
// in it far too early — and cutting mid-rune would send it invalid UTF-8.
func TestFitShortensByCharacters(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"ascii", strings.Repeat("a", stateLimit+1)},
		{"much too long", strings.Repeat("a", 4000)},
		{"multi byte", strings.Repeat("é", stateLimit+1)},
		{"four byte", strings.Repeat("🔊", 400)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := fit(tc.text)

			if n := utf8.RuneCountInString(got); n != stateLimit {
				t.Errorf("kept %d characters, want %d", n, stateLimit)
			}
			if !utf8.ValidString(got) {
				t.Error("cut through a character")
			}
			if !strings.HasSuffix(got, "…") {
				t.Errorf("does not say it was shortened: %q", got[len(got)-8:])
			}
		})
	}
}
