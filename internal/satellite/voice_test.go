package satellite

import (
	"testing"

	"github.com/ygelfand/echolocal/internal/wake"
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
	} {
		models := make([]wake.Model, 0, len(tc.installed))
		for _, id := range tc.installed {
			models = append(models, wake.Model{ID: id, Phrase: id})
		}

		available, active := wakeWords(models, WakeSlots)
		if len(available) != len(models) {
			t.Errorf("%s: offered %d models, want %d", name, len(available), len(models))
		}

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
