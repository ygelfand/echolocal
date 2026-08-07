package speaker

import (
	"fmt"
	"log/slog"
	"sync"
)

// Producer is a background sound: a track, or a room playing along with the house.
type Producer interface {
	Background

	// Duck sets the level for what is written next; what is already queued is Requeue's job.
	Duck(on bool)

	// Requeue re-scales what is already queued, which can be seconds of audio.
	Requeue()
}

// Arbiter is the one Background the driver holds, standing in for however many there are. The newest
// plays and the rest wait in order behind it, so when a track ends the stream it interrupted carries on.
type Arbiter struct {
	mu    sync.Mutex
	stack []Producer // the last is the one being heard
	held  bool       // the driver has stood the background down
	duck  bool
}

// Backgrounds is this driver's arbiter, made on first use. Per driver, not per package: echoctl
// builds its own.
func (d *Driver) Backgrounds() *Arbiter {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.arb == nil {
		d.arb = &Arbiter{}
		d.bg = d.arb // set here rather than through Yields, which wants the same lock
	}
	return d.arb
}

// Took is a producer starting. Whatever was playing stands down but keeps its place.
func (a *Arbiter) Took(p Producer) {
	a.mu.Lock()
	a.drop(p)
	stood := a.top()
	a.stack = append(a.stack, p)
	held, duck := a.held, a.duck
	a.mu.Unlock()

	// Already down if the driver holds the lot, and suspending twice would need undoing twice.
	if stood != nil && !held {
		slog.Debug("background handover", "from", kind(stood), "to", kind(p))
		stood.Suspend()
	}
	p.Duck(duck)
	if held {
		p.Suspend()
	}
}

// Gave is a producer finishing. Whatever it interrupted picks up again.
func (a *Arbiter) Gave(p Producer) {
	a.mu.Lock()
	was := a.top()
	a.drop(p)
	now := a.top()
	held := a.held
	a.mu.Unlock()

	if now == nil || now == was || held {
		return
	}
	slog.Debug("background resumed", "who", kind(now), "after", kind(p))
	now.Resume()
}

// Suspend and Resume are the Background the driver holds: a claim wants the speaker, so whichever
// producer is being heard stands down for it.
func (a *Arbiter) Suspend() {
	a.mu.Lock()
	if a.held {
		a.mu.Unlock()
		return
	}
	a.held = true
	p := a.top()
	a.mu.Unlock()

	if p != nil {
		p.Suspend()
	}
}

func (a *Arbiter) Resume() {
	a.mu.Lock()
	if !a.held {
		a.mu.Unlock()
		return
	}
	a.held = false
	p := a.top()
	a.mu.Unlock()

	if p != nil {
		p.Resume()
	}
}

// Duck quietens everything, waiting producers included, so one resuming mid-turn comes back quiet.
func (a *Arbiter) Duck(on bool) {
	a.mu.Lock()
	if a.duck == on {
		a.mu.Unlock()
		return
	}
	a.duck = on
	all := append([]Producer(nil), a.stack...)
	heard := a.top()
	a.mu.Unlock()

	for _, p := range all {
		p.Duck(on)
	}

	// Only the audible one, or the same samples get attenuated once per producer. Unducking is left
	// to drain.
	if on && heard != nil {
		heard.Requeue()
	}
}

// Playing is the producer being heard, or nil. For diagnostics.
func (a *Arbiter) Playing() Producer {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.top()
}

// top and drop are called with the lock held.
func (a *Arbiter) top() Producer {
	if len(a.stack) == 0 {
		return nil
	}
	return a.stack[len(a.stack)-1]
}

func (a *Arbiter) drop(p Producer) {
	for i, have := range a.stack {
		if have == p {
			a.stack = append(a.stack[:i], a.stack[i+1:]...)
			return
		}
	}
}

func kind(p Producer) string {
	if p == nil {
		return "nothing"
	}
	return fmt.Sprintf("%T", p)
}
