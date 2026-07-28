package led

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Priority is how the ring resolves being asked for two things at once. Higher wins, and when a
// claim goes away whatever is under it comes back on its own.
//
// This exists because the ring has one surface and several things with a legitimate claim on it: the
// boot animation, a conversation, a volume change, a failure, and whatever Home Assistant set the
// light to. Without an order they overwrite each other in whatever sequence the events happened to
// arrive, and a failure indication ends up cancelled by the teardown of the thing that failed.
type Priority int

const (
	// PriorityBase is the light entity: what the ring shows when nothing is happening.
	PriorityBase Priority = iota

	// PriorityTurn is a conversation, which holds the ring from the wake word to the end of the reply.
	PriorityTurn

	// PriorityNotice is a brief acknowledgement of something the user just did, like a volume change.
	// It outranks a conversation because it is a direct response to a button they are holding.
	PriorityNotice

	// PriorityTrouble is a failure. It outranks a conversation so that ending the turn that failed
	// cannot take the indication away with it.
	PriorityTrouble

	// PriorityBoot is the start-up animation, which owns the ring until the device is actually able
	// to answer. Nothing else may write while it is up.
	PriorityBoot
)

// Content is what a claim wants shown. An empty Content shows nothing, which is how a claim can
// exist before it has anything to say.
type Content struct {
	// Frame is a still image, used when Effect is empty.
	Frame []Color

	// Effect is a named animation, and Base the colour it works from.
	Effect  string
	Base    Color
	Reverse bool

	// Animate replaces both, for something with a timeline of its own such as the boot animation. It
	// runs until the context is cancelled.
	Animate func(ctx context.Context, r *Ring) error
}

func (c Content) empty() bool {
	return c.Animate == nil && c.Effect == "" && len(c.Frame) == 0
}

// Driver owns the ring. One goroutine writes to the hardware; everything else takes a Claim and says
// what it wants, which removes the question of who painted last.
type Driver struct {
	ring *Ring

	mu     sync.Mutex
	claims []*Claim
	seq    uint64

	// changed wakes the render loop. Buffered, so a change that lands between choosing what to
	// render and starting to watch for changes is not lost.
	changed chan struct{}
}

func NewDriver(r *Ring) *Driver {
	return &Driver{ring: r, changed: make(chan struct{}, 1)}
}

// Name is what it is called when supervised.
func (d *Driver) Name() string { return "ring" }

// Claim asks for the ring at a priority. Nothing is shown until the claim is given content, and
// whatever it shows lasts until it is released or something higher takes over.
func (d *Driver) Claim(p Priority) *Claim {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.seq++
	c := &Claim{d: d, priority: p, seq: d.seq}
	d.claims = append(d.claims, c)

	// Nothing to show yet, so the loop does not need waking.
	return c
}

// Run drives the ring until ctx is cancelled. Nothing reaches the hardware without going through it.
func (d *Driver) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}

		top, rev := d.top()
		d.render(ctx, top, rev)

		// A timed claim that has run its course goes away, revealing what is under it.
		if top != nil && top.done() {
			top.Release()
		}
	}
}

// render shows one claim until something changes: a claim appears, is given different content,
// expires, or is released.
func (d *Driver) render(ctx context.Context, top *Claim, rev uint64) {
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if top != nil {
		if until, ok := top.deadline(); ok {
			rctx, cancel = context.WithDeadline(ctx, until)
			defer cancel()
		}
	}

	// Watch for a change alongside the render, so a new claim interrupts an animation part way
	// through rather than after it finishes.
	//
	// Only a change to what should be showing counts. A claim underneath being repainted — Home
	// Assistant setting the light, the saved volume arriving — must not restart the animation on top
	// of it, or the boot walk begins again every time anything else so much as reports its state.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-d.changed:
				if now, nowRev := d.top(); now != top || nowRev != rev {
					cancel()
					return
				}
			case <-rctx.Done():
				return
			case <-stop:
				return
			}
		}
	}()

	if top == nil {
		if err := d.ring.Off(); err != nil {
			slog.Error("ring blank failed", "err", err)
		}
		<-rctx.Done()
		return
	}
	d.show(rctx, top.get())
}

func (d *Driver) show(ctx context.Context, c Content) {
	switch {
	case c.Animate != nil:
		if err := c.Animate(ctx, d.ring); err != nil && ctx.Err() == nil {
			slog.Error("ring animation failed", "err", err)
		}
		// An animation that finishes early holds what it left until something changes.
		<-ctx.Done()

	case c.Effect != "":
		var err error
		if c.Reverse {
			err = RunEffectReversed(ctx, d.ring, c.Effect, c.Base)
		} else {
			err = RunEffect(ctx, d.ring, c.Effect, c.Base)
		}
		if err != nil && ctx.Err() == nil {
			slog.Error("ring effect failed", "effect", c.Effect, "err", err)
			<-ctx.Done()
		}

	default:
		if err := d.ring.SetSegments(c.Frame); err != nil {
			slog.Error("ring write failed", "err", err)
		}
		<-ctx.Done()
	}
}

// top is what should be showing: the highest priority claim with something to show, and among equals
// the one taken most recently. The revision comes back with it so the caller can tell this claim
// changing from something underneath changing.
func (d *Driver) top() (*Claim, uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Each claim's own lock guards its content, so it is taken here too. The order is always the
	// driver's lock first and never the other way round.
	live := make([]*Claim, 0, len(d.claims))
	for _, c := range d.claims {
		if c.live() {
			live = append(live, c)
		}
	}
	if len(live) == 0 {
		return nil, 0
	}

	sort.SliceStable(live, func(i, j int) bool {
		if live[i].priority != live[j].priority {
			return live[i].priority > live[j].priority
		}
		return live[i].seq > live[j].seq
	})

	best := live[0]
	best.mu.Lock()
	defer best.mu.Unlock()
	return best, best.rev
}

// wake tells the render loop to reconsider.
func (d *Driver) wake() {
	select {
	case d.changed <- struct{}{}:
	default:
	}
}

func (d *Driver) drop(c *Claim) {
	d.mu.Lock()
	for i, held := range d.claims {
		if held == c {
			d.claims = append(d.claims[:i], d.claims[i+1:]...)
			break
		}
	}
	d.mu.Unlock()
	d.wake()
}

// Claim is one thing's hold on the ring. It is safe to use from any goroutine, and safe to keep
// after releasing: everything on a released claim does nothing.
type Claim struct {
	d        *Driver
	priority Priority
	seq      uint64

	mu       sync.Mutex
	content  Content
	expires  time.Time
	released bool

	// rev counts content changes, so the driver can tell being woken about this claim from being
	// woken about something else.
	rev uint64
}

// Show puts something on the ring and leaves it there.
func (c *Claim) Show(content Content) { c.set(content, time.Time{}) }

// ShowFor puts something on the ring for a while, after which the claim releases itself and whatever
// is underneath comes back.
func (c *Claim) ShowFor(content Content, d time.Duration) {
	c.set(content, time.Now().Add(d))
}

// Paint shows a still frame.
func (c *Claim) Paint(frame []Color) { c.Show(Content{Frame: frame}) }

// PaintFor shows a still frame briefly.
func (c *Claim) PaintFor(frame []Color, d time.Duration) { c.ShowFor(Content{Frame: frame}, d) }

// Play runs a named effect.
func (c *Claim) Play(effect string, base Color) { c.Show(Content{Effect: effect, Base: base}) }

// PlayReversed runs it the other way round the ring, which is how the device says it has stopped
// listening and is waiting on an answer.
func (c *Claim) PlayReversed(effect string, base Color) {
	c.Show(Content{Effect: effect, Base: base, Reverse: true})
}

// Clear gives up the ring without releasing the claim, for a holder that is still around but has
// nothing to say.
func (c *Claim) Clear() { c.set(Content{}, time.Time{}) }

func (c *Claim) set(content Content, expires time.Time) {
	c.mu.Lock()
	if c.released {
		c.mu.Unlock()
		return
	}
	c.content, c.expires = content, expires
	c.rev++
	c.mu.Unlock()

	c.d.wake()
}

// Release hands the ring back.
func (c *Claim) Release() {
	c.mu.Lock()
	if c.released {
		c.mu.Unlock()
		return
	}
	c.released = true
	c.content = Content{}
	c.mu.Unlock()

	c.d.drop(c)
}

// live reports whether the claim is still held and has something to show.
func (c *Claim) live() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.released && !c.content.empty()
}

func (c *Claim) get() Content {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.content
}

func (c *Claim) deadline() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expires, !c.expires.IsZero()
}

// done reports whether a timed claim has run out.
func (c *Claim) done() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.expires.IsZero() && !time.Now().Before(c.expires)
}
