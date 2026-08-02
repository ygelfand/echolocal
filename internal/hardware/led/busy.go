package led

import (
	"log/slog"
	"slices"
	"sync"
	"time"
)

// Busy is the ring saying the device is working on something somebody is waiting on.
//
// A motion rather than a bar: the waits worth showing have no measurable progress, so a bar would
// have to invent a number. The colour says which work.
//
// Several may run at once. The most recent shows, and the ring goes back to whatever was underneath
// when the last finishes.

// Work is a kind of thing the device does that is worth showing. The colours are what distinguishes
// them, and they are provisional: the only way to choose them is to look at the ring.
type Work int

const (
	// WorkWakeWord is a newly chosen wake word being fetched, loaded, and warmed up.
	WorkWakeWord Work = iota

	// WorkElsewhere is another device answering a wake word this one also heard.
	WorkElsewhere

	// WorkUpdate is echod replacing itself.
	WorkUpdate
)

// UpdateColor is what the ring is left showing while the process is gone: the same teal as the work
// that got it there, held still because nothing is running to animate it.
var UpdateColor = Color{R: 0x00, G: 0xB0, B: 0xC0}

// busyLimit is the longest any one piece of work may hold the ring. Whatever ends a busy indication is
// a signal from elsewhere, and a signal that never arrives would otherwise animate the device for
// ever.
const busyLimit = time.Minute

// appearance is the motion and colour for a kind of work. One motion for all of them on purpose: it
// means "working", and it is the colour that says at what.
func (w Work) appearance() (string, Color) {
	switch w {
	case WorkWakeWord:
		return EffectChase, Color{R: 0xFF, G: 0x9A, B: 0x00}
	case WorkElsewhere:
		return EffectChase, Color{R: 0x80, G: 0x40, B: 0xC0}
	case WorkUpdate:
		return EffectChase, UpdateColor
	}
	return EffectChase, Color{R: 0xFF, G: 0xFF, B: 0xFF}
}

func (w Work) String() string {
	switch w {
	case WorkWakeWord:
		return "wake word"
	case WorkElsewhere:
		return "answered elsewhere"
	case WorkUpdate:
		return "update"
	}
	return "work"
}

type Busy struct {
	claim *Claim

	mu    sync.Mutex
	tasks []*Task
}

// Task is one piece of work in progress, and the only way to end it.
type Task struct {
	busy  *Busy
	work  Work
	timer *time.Timer
}

// Start shows that work is under way. Whoever starts it owns ending it.
func (b *Busy) Start(w Work) *Task {
	b.mu.Lock()
	defer b.mu.Unlock()

	t := &Task{busy: b, work: w}
	t.timer = time.AfterFunc(busyLimit, func() {
		slog.Warn("giving up a busy indication nothing finished", "work", w, "after", busyLimit)
		t.Done()
	})

	b.tasks = append(b.tasks, t)
	b.show()
	return t
}

// Flash shows work that is already over, for as long as it takes to be noticed. Same appearance and
// same priority as work in progress, because to whoever is looking it is the same kind of thing: the
// device saying what it is doing about what they just said.
func (b *Busy) Flash(w Work, d time.Duration) {
	t := b.Start(w)
	time.AfterFunc(d, t.Done)
}

// Done ends one piece of work. Safe to call twice, and on nothing, so a caller may both defer it and
// end the work early.
func (t *Task) Done() {
	if t == nil {
		return
	}

	t.busy.mu.Lock()
	defer t.busy.mu.Unlock()

	i := slices.Index(t.busy.tasks, t)
	if i < 0 {
		return
	}
	t.timer.Stop()
	t.busy.tasks = slices.Delete(t.busy.tasks, i, i+1)
	t.busy.show()
}

// Running reports whether anything is working, for whoever wants to know without holding a task.
func (b *Busy) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	return len(b.tasks) > 0
}

// show puts up the most recent work, or gives the ring back when there is none left. The most recent
// rather than the first: it is the one the user just asked for. Held with mu.
func (b *Busy) show() {
	if len(b.tasks) == 0 {
		b.claim.Clear()
		return
	}
	b.claim.Play(b.tasks[len(b.tasks)-1].work.appearance())
}
