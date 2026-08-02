package hook

import (
	"sync"
	"testing"
)

// The reason this exists: a callback field holds one listener, and the second thing to want the same
// event silently replaces the first.
func TestEveryListenerHears(t *testing.T) {
	var h Hook[int]

	var got []int
	h.Listen(func(v int) { got = append(got, v) })
	h.Listen(func(v int) { got = append(got, v*10) })

	h.Emit(1)

	if len(got) != 2 {
		t.Fatalf("%d listeners heard it, want 2", len(got))
	}
}

func TestCancelStopsOneListener(t *testing.T) {
	var h Hook[string]

	var kept, dropped int
	h.Listen(func(string) { kept++ })
	cancel := h.Listen(func(string) { dropped++ })

	h.Emit("a")
	cancel()
	h.Emit("b")

	if kept != 2 {
		t.Errorf("the kept listener heard %d, want 2", kept)
	}
	if dropped != 1 {
		t.Errorf("the cancelled listener heard %d, want 1", dropped)
	}
	if n := h.Listeners(); n != 1 {
		t.Errorf("%d listeners remain, want 1", n)
	}
}

// A hook nobody is listening to is the normal state of most hardware, and must not panic.
func TestEmitWithNoListeners(t *testing.T) {
	var h Hook[int]
	h.Emit(1)
}

// The lock must not be held while listeners run, or one that subscribes or cancels from inside a
// handler deadlocks the reader that emitted.
func TestListenerMaySubscribeFromInside(t *testing.T) {
	var h Hook[int]

	done := make(chan struct{})
	h.Listen(func(int) {
		h.Listen(func(int) {})
		close(done)
	})

	h.Emit(1)
	<-done
}

func TestConcurrentListenAndEmit(t *testing.T) {
	var h Hook[int]
	var wg sync.WaitGroup

	for range 8 {
		wg.Add(2)
		go func() { defer wg.Done(); h.Listen(func(int) {}) }()
		go func() { defer wg.Done(); h.Emit(1) }()
	}
	wg.Wait()
}
