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
	// down is every producer the arbiter has suspended and not yet answered. A claim and a handover
	// can stand different producers down at the same time — the one the claim caught, and the one a
	// takeover displaced — so a single "the hold stood this one down" identity cannot say which of
	// them a retake answers for. Producers are pointers, and the stack itself compares them by
	// identity — see drop — so they are safe as keys here.
	//
	// The producers are called under mu by whichever method decides on them, so a decision and its
	// delivery are one step and a suspend can never land on a producer that has already left.
	down map[Producer]bool
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
//
// A retake — the same producer starting again while it is still in the stack — changes nothing: it
// keeps the standing it carried, down or not. Only a producer joining afresh has to be dealt with.
//
// The stand-downs are delivered under the lock, so a decision and its delivery are one step: nothing
// can stop or join between them and put a suspend on a producer that has already left. The producers
// themselves never call back into the arbiter, so the lock is safe to hold across them (uses of the
// media stream always release it before touching the arbiter — start, Stop and finished all do).
func (a *Arbiter) Took(p Producer) {
	a.mu.Lock()
	member := a.drop(p)
	stood := a.top()
	a.stack = append(a.stack, p)
	held, duck := a.held, a.duck

	if stood != nil && !held {
		slog.Debug("background handover", "from", kind(stood), "to", kind(p))
		if a.note(stood) {
			stood.Suspend()
		}
	}
	// A retake is not suspended again here: whatever stands it down, this arbiter already owes the
	// Resume that answers it, and the ledger above says so. Anyone else joining mid-hold is stood
	// down so it cannot play over the claim.
	if held && !member && a.note(p) {
		p.Suspend()
	}
	a.mu.Unlock()

	p.Duck(duck)
}

// Gave is a producer finishing. Whatever it interrupted picks up again.
//
// The leaver takes its answer with it: if this arbiter had suspended it, the one Resume it still owes
// is delivered here, so nothing a producer is waiting on outlives its own end. The producer that
// surfaces takes over only if the claim has not, and only if it was stood down for the departure.
// Both deliveries happen under the lock, like Took's — a claim ending or a producer stopping cannot
// cut across them and leave a count stranded.
func (a *Arbiter) Gave(p Producer) {
	a.mu.Lock()
	if a.down[p] {
		delete(a.down, p)
		p.Resume()
	}
	a.drop(p)
	now := a.top()
	held := a.held

	if !held && now != nil && a.down[now] {
		delete(a.down, now)
		slog.Debug("background resumed", "who", kind(now), "after", kind(p))
		now.Resume()
	}
	a.mu.Unlock()
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
	if p := a.top(); p != nil && a.note(p) {
		p.Suspend()
	}
	a.mu.Unlock()
}

func (a *Arbiter) Resume() {
	a.mu.Lock()
	if !a.held {
		a.mu.Unlock()
		return
	}
	a.held = false
	if p := a.top(); p != nil && a.down[p] {
		delete(a.down, p)
		p.Resume()
	}
	a.mu.Unlock()
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

// top, drop and note are called with the lock held.
func (a *Arbiter) top() Producer {
	if len(a.stack) == 0 {
		return nil
	}
	return a.stack[len(a.stack)-1]
}

// drop removes p and says whether it was there.
func (a *Arbiter) drop(p Producer) bool {
	for i, have := range a.stack {
		if have == p {
			a.stack = append(a.stack[:i], a.stack[i+1:]...)
			return true
		}
	}
	return false
}

// note records that the arbiter has suspended p, saying whether that is new. down is made on first
// use because the tests build &Arbiter{} by hand.
func (a *Arbiter) note(p Producer) bool {
	if a.down == nil {
		a.down = map[Producer]bool{}
	}
	if a.down[p] {
		return false
	}
	a.down[p] = true
	return true
}

func kind(p Producer) string {
	if p == nil {
		return "nothing"
	}
	return fmt.Sprintf("%T", p)
}
