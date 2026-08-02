// Package hook is how hardware tells whoever is interested that something happened.
//
// A hardware package does not know what cares about it — a button is a button whether the device
// answers it, logs it, or both — so it offers a hook rather than a callback field. A callback field
// holds one listener and the second one to be wired silently replaces the first.
package hook

import "sync"

// Hook carries a value to everyone listening.
//
// Emit runs the listeners on whatever goroutine reached the hardware, which is usually a reader with
// a device behind it: a listener that blocks stalls that reader, and a driver whose queue overflows
// takes more than this hook with it. So listeners must not block. Anything slow belongs on the far
// side of a channel the listener owns.
type Hook[T any] struct {
	mu   sync.Mutex
	next int
	fns  map[int]func(T)
}

// Listen adds a listener and returns the function that removes it. Listeners are not ordered: a hook
// says something happened, it does not run a pipeline.
func (h *Hook[T]) Listen(fn func(T)) (cancel func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.fns == nil {
		h.fns = map[int]func(T){}
	}
	id := h.next
	h.next++
	h.fns[id] = fn

	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.fns, id)
	}
}

// Emit tells everyone listening. The lock is not held while they run, so a listener may subscribe or
// cancel from inside one.
func (h *Hook[T]) Emit(v T) {
	h.mu.Lock()
	fns := make([]func(T), 0, len(h.fns))
	for _, fn := range h.fns {
		fns = append(fns, fn)
	}
	h.mu.Unlock()

	for _, fn := range fns {
		fn(v)
	}
}

// Listeners is how many are subscribed, for diagnostics and tests.
func (h *Hook[T]) Listeners() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.fns)
}
