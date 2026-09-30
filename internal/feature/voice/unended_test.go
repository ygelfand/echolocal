package voice

import (
	"testing"

	"github.com/ygelfand/echolocal/internal/config"
)

// A turn whose talk never ended - a television or a video that said the wake word and kept on talking -
// ends the way the slot's setting says. Everything else that runs out of time is what it always was.
func TestATimeoutEndsTheWayTheSlotSays(t *testing.T) {
	for name, tc := range map[string]struct {
		unended  config.Unended
		phase    phase
		followUp bool
		speech   bool
		want     ending
	}{
		// Home Assistant heard speech start and never heard it end: listening ran out with everything
		// working, so the slot's setting decides
		"talk never ended, alert":         {config.UnendedAlert, phaseListening, false, true, endTrouble},
		"talk never ended, quiet":         {config.UnendedQuiet, phaseListening, false, true, endQuiet},
		"talk never ended, send":          {config.UnendedSend, phaseListening, false, true, endSend},
		"talk never ended, saved before":  {"", phaseListening, false, true, endTrouble},
		"talk never ended, unknown value": {"shout", phaseListening, false, true, endTrouble},
		// not even speech reported: Home Assistant may have stopped answering, whatever the setting
		"Home Assistant said nothing, quiet": {config.UnendedQuiet, phaseListening, false, false, endTrouble},
		"Home Assistant said nothing, send":  {config.UnendedSend, phaseListening, false, false, endTrouble},
		// a follow-up nobody answered ends quietly, as it always has
		"nothing followed a reply":            {config.UnendedAlert, phaseListening, true, false, endQuiet},
		"a follow-up that kept talking":       {config.UnendedAlert, phaseListening, true, true, endQuiet},
		"a follow-up that kept talking, send": {config.UnendedSend, phaseListening, true, true, endQuiet},
		// the question was heard and the answer never came: a failure under every setting
		"gave up waiting for an answer, quiet": {config.UnendedQuiet, phaseThinking, false, true, endTrouble},
		"gave up waiting for an answer, send":  {config.UnendedSend, phaseThinking, false, true, endTrouble},
	} {
		if got := onTimeout(tc.unended, tc.phase, tc.followUp, tc.speech); got != tc.want {
			t.Errorf("%s: onTimeout = %v, want %v", name, got, tc.want)
		}
	}
}

// Home Assistant saying the audio held no words is a wake word the room did not mean. Alert keeps the
// trouble tone; Quiet and Send end without it (under Send, Home Assistant already had its say and
// found nothing). Every other error is something breaking and sounds whatever the setting.
func TestNoWordsFollowsTheSlotOtherErrorsAlwaysSound(t *testing.T) {
	for name, tc := range map[string]struct {
		unended config.Unended
		code    string
		quiet   bool
	}{
		"no words, alert":        {config.UnendedAlert, errNoText, false},
		"no words, quiet":        {config.UnendedQuiet, errNoText, true},
		"no words, send":         {config.UnendedSend, errNoText, true},
		"no words, saved before": {"", errNoText, false},
		"stt failed, quiet":      {config.UnendedQuiet, "stt-stream-failed", false},
		"intent failed, send":    {config.UnendedSend, "intent-failed", false},
		"tts failed, quiet":      {config.UnendedQuiet, "tts-failed", false},
		"no code, quiet":         {config.UnendedQuiet, "", false},
	} {
		if got := quietError(tc.unended, tc.code); got != tc.quiet {
			t.Errorf("%s: quietError = %v, want %v", name, got, tc.quiet)
		}
	}
}
