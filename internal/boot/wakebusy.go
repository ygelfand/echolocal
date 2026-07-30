package boot

import (
	"sync"

	"github.com/ygelfand/echolocal/internal/led"
)

// wakeBusy shows the ring working from the moment a wake word selection arrives until every slot it
// loaded can actually hear.
//
// Three things happen in that gap and only the first is quick: the model is fetched if this device has
// never had it, the engine loads it, and then its front end fills — which takes audio rather than time,
// so it is the engine reporting a score that ends this, not a guessed delay. Somebody testing a word
// they just chose is doing it in exactly that window.
type wakeBusy struct {
	busy *led.Busy

	// ready asks the engine whether a slot has scored already, for the slots that warmed up while the
	// rest of the selection was still being fetched.
	ready func(slot int) bool

	mu   sync.Mutex
	task *led.Task

	// waiting is whether the slots to wait on are known yet. Until they are, an empty pending set means
	// the selection is still being acted on rather than finished: a model that had to be fetched can
	// take longer than another slot takes to warm up, so a score can arrive before this knows what it
	// is waiting for.
	waiting bool
	pending map[int]bool
}

func newWakeBusy(busy *led.Busy, ready func(slot int) bool) *wakeBusy {
	return &wakeBusy{busy: busy, ready: ready, pending: map[int]bool{}}
}

// begin is the start of acting on a selection, before anything is fetched: the download is part of what
// is being waited on.
func (w *wakeBusy) begin() {
	w.mu.Lock()
	defer w.mu.Unlock()

	// A second selection while the first is still warming up is one indication, and waits for the slots
	// it names rather than for the ones the last one did.
	w.waiting = false
	if w.task == nil {
		w.task = w.busy.Start(led.WorkWakeWord)
	}
}

// waitFor names the slots that were loaded, which are the ones that have to score before the ring goes
// back to what was underneath. Slots that scored while the selection was still being acted on are not
// waited for again.
func (w *wakeBusy) waitFor(slots []int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.pending, w.waiting = map[int]bool{}, true
	for _, n := range slots {
		if !w.ready(n) {
			w.pending[n] = true
		}
	}
	w.settle()
}

// scored is one slot reporting its first score.
func (w *wakeBusy) scored(slot int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	delete(w.pending, slot)
	w.settle()
}

// settle ends the indication once nothing is outstanding. Held with mu.
func (w *wakeBusy) settle() {
	if w.task == nil || !w.waiting || len(w.pending) > 0 {
		return
	}
	w.task.Done()
	w.task, w.waiting = nil, false
}
