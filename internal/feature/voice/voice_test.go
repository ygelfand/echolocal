package voice

import (
	"testing"

	"github.com/ygelfand/echolocal/internal/feature/wakeword"
	"github.com/ygelfand/echolocal/internal/lib/wake"
)

// What a device advertises as active when nothing has been chosen decides whether a fresh install can
// be spoken to at all, and it must not come down to which model sorts first.
func TestWakeWordsPreselectsTheDefault(t *testing.T) {
	for name, tc := range map[string]struct {
		installed []string
		want      string
	}{
		"the default is installed":     {[]string{"hey_jarvis", wake.DefaultModel, "hey_mycroft"}, wake.DefaultModel},
		"the default sorts last":       {[]string{"alexa", wake.DefaultModel}, wake.DefaultModel},
		"the default is not installed": {[]string{"hey_jarvis"}, "hey_jarvis"},
		"nothing installed":            {nil, ""},
		"Pryon install prefers Alexa":  {[]string{wake.DefaultModel, wake.PryonID}, wake.PryonID},
	} {
		models := make([]wake.Model, 0, len(tc.installed))
		for _, id := range tc.installed {
			models = append(models, wake.Model{ID: id, Phrase: id})
		}

		active := activeWakeWords(models, wakeword.Slots)

		switch {
		case tc.want == "":
			if len(active) != 0 {
				t.Errorf("%s: listening for %v with nothing installed", name, active)
			}
		case len(active) != 1 || active[0] != tc.want:
			t.Errorf("%s: listening for %v, want just %q", name, active, tc.want)
		}
	}
}
