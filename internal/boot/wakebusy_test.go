package boot

import (
	"testing"

	"github.com/ygelfand/echolocal/internal/hardware/led"
)

// warm stands in for the engine: which slots have scored, and nothing else.
type warm map[int]bool

func fixture(t *testing.T, scored warm) (*wakeBusy, *led.Busy) {
	t.Helper()

	busy := led.NewDriver(nil).Busy()
	return newWakeBusy(busy, func(slot int) bool { return scored[slot] }), busy
}

func TestWakeBusyHoldsUntilEverySlotScores(t *testing.T) {
	w, busy := fixture(t, warm{})

	w.begin()
	if !busy.Running() {
		t.Fatal("nothing showing while a selection is being acted on")
	}

	w.waitFor([]int{0, 1})
	w.scored(0)
	if !busy.Running() {
		t.Error("stopped with slot 2 still unable to hear")
	}

	w.scored(1)
	if busy.Running() {
		t.Error("still showing once both slots can hear")
	}
}

// A selection that loaded nothing has nothing to warm up.
func TestWakeBusyStopsWhenNothingLoaded(t *testing.T) {
	w, busy := fixture(t, warm{})

	w.begin()
	w.waitFor(nil)

	if busy.Running() {
		t.Error("still showing after a selection that loaded nothing")
	}
}

// A slot whose wake word was already loaded and warm is not waited for again: it would never report a
// first score, and the ring would animate until the timeout.
func TestWakeBusyDoesNotWaitOnSlotsAlreadyWarm(t *testing.T) {
	w, busy := fixture(t, warm{0: true})

	w.begin()
	w.waitFor([]int{0})

	if busy.Running() {
		t.Error("waiting on a slot that has already scored")
	}
}

// A model that had to be fetched can take longer than another slot takes to warm up, so a score can
// arrive before the selection has finished being acted on.
func TestWakeBusyHandlesAScoreBeforeTheSlotsAreKnown(t *testing.T) {
	scored := warm{}
	w, busy := fixture(t, scored)

	w.begin()
	scored[0] = true
	w.scored(0)

	w.waitFor([]int{0, 1})
	if !busy.Running() {
		t.Fatal("stopped with slot 2 still unable to hear")
	}

	w.scored(1)
	if busy.Running() {
		t.Error("still showing once both slots can hear")
	}
}

// Two selections in a row are one indication, not two: the second arrives while the first is still
// warming up, and the ring should not be left holding a task nobody will finish.
func TestWakeBusyBeginTwiceIsOneIndication(t *testing.T) {
	w, busy := fixture(t, warm{})

	w.begin()
	w.waitFor([]int{0})
	w.begin()
	w.waitFor([]int{0})

	w.scored(0)
	if busy.Running() {
		t.Error("still showing after the slot scored")
	}
}
