package activity

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"time"

	"github.com/ygelfand/echolocal/internal/component"
)

// TurnEvent is what Home Assistant's bus calls a finished turn. Under the esphome domain because that
// is the only one Home Assistant accepts from a device.
const TurnEvent = "esphome.echolocal_turn"

// Version is bumped when a field changes meaning, so a reader that does not know a version can ignore
// it rather than guess.
const Version = "1"

// Outcome is how a turn ended, which is not always visible from what it left behind: a turn that timed
// out and one that was answered can both have a transcript and no reply.
type Outcome string

const (
	Completed Outcome = "completed"
	Cancelled Outcome = "cancelled"
	Timeout   Outcome = "timeout"
	Failed    Outcome = "failed"
)

// Turn is one turn as it happens. Each phase stamps when it began and nothing is worked out until the
// turn closes, so the arithmetic lives in one place instead of at every boundary.
type Turn struct {
	mu sync.Mutex

	id   string
	slot int
	word string

	heard string
	reply string

	// audio is seconds of recording kept for this turn, zero when none was.
	audio float64

	// When each phase began, zero for one the turn never reached, and when it ended.
	listening time.Time
	thinking  time.Time
	replying  time.Time
	ended     time.Time
}

// Begin starts a turn. The id lives as long as the device holds the turn's audio, which is what asks
// for it later.
func (l *Log) Begin(slot int, word string) *Turn {
	return &Turn{id: newID(), slot: slot, word: word}
}

// Listening says the device is taking audio.
func (t *Turn) Listening() {
	if t == nil {
		return
	}
	t.at(&t.listening)
}

// Heard says Home Assistant has enough audio and is working on an answer.
func (t *Turn) Heard(text string) {
	if t == nil {
		return
	}

	t.mu.Lock()
	t.heard = text
	t.mu.Unlock()

	t.at(&t.thinking)
}

// Replying says an answer has arrived and is being spoken.
func (t *Turn) Replying(text string) {
	if t == nil {
		return
	}

	t.mu.Lock()
	t.reply = text
	t.mu.Unlock()

	t.at(&t.replying)
}

// Ends closes the turn and reports it. Only the first call counts: several things can notice a turn is
// over, and the one that noticed first is the one that timed it.
func (t *Turn) Ends(how Outcome) {
	if t == nil {
		return
	}

	t.mu.Lock()
	if !t.ended.IsZero() {
		t.mu.Unlock()
		return
	}
	t.ended = time.Now()
	fields := t.fields(how)
	t.mu.Unlock()

	component.Fire.Emit(component.Event{Name: TurnEvent, Data: fields})
}

// Records says how long a recording was kept for this turn, which is what decides whether anything
// offers to play it. Zero for a turn nothing was kept for.
func (t *Turn) Records(seconds float64) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.audio = seconds
}

// ID is what asks the device for this turn's audio.
func (t *Turn) ID() string {
	if t == nil {
		return ""
	}
	return t.id
}

// at stamps a phase, unless the turn has closed or that phase has already begun — Home Assistant can
// say the same thing twice, and the first time is when it happened.
func (t *Turn) at(when *time.Time) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ended.IsZero() && when.IsZero() {
		*when = time.Now()
	}
}

// fields is the event's data. Everything is a string: the wire carries no other kind, so numbers go
// as decimal and a reader turns them back.
func (t *Turn) fields(how Outcome) map[string]string {
	out := map[string]string{
		"version": Version,
		"id":      t.id,
		"slot":    strconv.Itoa(t.slot),
		"outcome": string(how),
	}

	if t.word != "" {
		out["wake_word"] = component.FitEvent(t.word)
	}
	if t.heard != "" {
		out["heard"] = component.FitEvent(t.heard)
	}
	if t.reply != "" {
		out["reply"] = component.FitEvent(t.reply)
	}
	if t.audio > 0 {
		out["audio_seconds"] = strconv.FormatFloat(t.audio, 'f', 1, 64)
	}

	// A phase runs until the next one the turn reached, or until it ended. One it never reached has no
	// duration rather than a zero, so a turn that failed while thinking reports no speaking time.
	marks := []struct {
		key string
		at  time.Time
	}{
		{"listen_ms", t.listening},
		{"think_ms", t.thinking},
		{"speak_ms", t.replying},
	}

	for i, mark := range marks {
		if mark.at.IsZero() {
			continue
		}

		until := t.ended
		for _, next := range marks[i+1:] {
			if !next.at.IsZero() {
				until = next.at
				break
			}
		}
		if ms := until.Sub(mark.at).Milliseconds(); ms > 0 {
			out[mark.key] = strconv.FormatInt(ms, 10)
		}
	}
	return out
}

// The id outlives its recording, in every logbook entry the recorder keeps, so it has to stay unique
// across all of them.
func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}
