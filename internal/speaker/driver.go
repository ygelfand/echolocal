package speaker

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Driver decides who gets to make a sound, the way led.Driver decides what the ring shows. It does
// not touch the hardware: Player owns the device and the queue, and this owns who may fill it.
//
// One claim holds it at a time, and taking it silences whatever had it. The device has several
// sources of sound — a wake tone, a reply fetched over HTTP, a reply streamed over the API, an
// announcement — and nothing used to represent the one that was playing, so stopping it meant
// reaching into each of them, and the ones nobody reached went on making noise. A claim covers its
// whole errand, fetching included, so cancelling a reply abandons the download rather than dropping
// the audio once it arrives.
type Driver struct {
	p *Player

	mu  sync.Mutex
	now *Claim
	bg  Background
}

func NewDriver(p *Player) *Driver { return &Driver{p: p} }

// Background is a long sound that yields to the others rather than being taken from: media, which
// plays for minutes and cannot be started again from where it was. Everything else is an errand
// that runs to the end, so a claim is enough for it.
type Background interface {
	// Suspend stops filling the queue and empties what is in it. It is called before the claim that
	// displaced it queues anything, so the two never fight over the same audio.
	Suspend()

	// Resume carries on, if the caller is still what it was suspended for.
	Resume()
}

// Yields registers the background sound. There is one.
func (d *Driver) Yields(b Background) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bg = b
}

// Claim takes the speaker and does whatever it takes to make the sound: play queues audio and
// returns, and the claim ends once what it queued has played out. It must return when ctx is done,
// which is what being silenced means.
func (d *Driver) Claim(name string, play func(ctx context.Context, p *Player) error) *Claim {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Claim{name: name, cancel: cancel, done: make(chan struct{})}

	d.mu.Lock()
	previous, bg := d.now, d.bg
	d.now = c
	d.mu.Unlock()

	if bg != nil {
		bg.Suspend()
	}
	previous.preempt(d.p)

	go func() {
		defer close(c.done)
		defer d.release(c)

		if err := play(ctx, d.p); err != nil {
			c.fail(err)
			return
		}
		c.mark(d.await(ctx))
	}()
	return c
}

// release lets the background sound carry on, once nothing else wants the speaker. A claim that was
// displaced releases nothing: the one that took it from it is still playing.
func (d *Driver) release(c *Claim) {
	d.mu.Lock()
	bg := d.bg
	if d.now != c {
		d.mu.Unlock()
		return
	}
	d.now = nil
	d.mu.Unlock()

	if bg != nil {
		bg.Resume()
	}
}

// Interject makes a sound without taking the speaker from what has it. Short feedback — a volume
// beep, a mute tone — is worth hearing, and not worth losing a reply over: it goes into the queue
// behind whatever is already there rather than replacing it.
func (d *Driver) Interject(play func(p *Player)) { play(d.p) }

// Silence stops whatever is playing and empties the queue whether anything claimed it or not. It is
// safe when nothing is playing.
func (d *Driver) Silence() {
	d.mu.Lock()
	c := d.now
	d.now = nil
	d.mu.Unlock()

	c.stop(d.p)
	d.p.Drain()
}

// Busy reports whether anything holds the speaker.
func (d *Driver) Busy() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.now != nil && !d.now.Finished()
}

// HardwareTail is how long the playback buffer goes on sounding after the queue has run out, so
// nothing should conclude the room is quiet until it has passed.
const HardwareTail = 150 * time.Millisecond

// dry is how long the queue has to stay empty to count as finished. Audio arrives in chunks with gaps
// between them, so one empty read means nothing.
const dry = 400 * time.Millisecond

// await waits for what was queued to play out, and reports when the queue first ran dry: what comes
// after that is confirmation, and counting it would overstate how long the sound took.
func (d *Driver) await(ctx context.Context) time.Time {
	const tick = 50 * time.Millisecond

	var empty time.Time
	for {
		select {
		case <-ctx.Done():
			return time.Now()
		case <-time.After(tick):
		}

		if d.p.Queued() > 0 {
			empty = time.Time{}
			continue
		}
		if empty.IsZero() {
			empty = time.Now()
		}
		if time.Since(empty) >= dry {
			time.Sleep(HardwareTail)
			return empty
		}
	}
}

// Claim is a hold on the speaker.
type Claim struct {
	name   string
	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	err      error
	stopped  bool
	started  time.Time
	finished time.Time
}

// Started records that sound has begun, which is where a reply's timing starts counting from.
func (c *Claim) Started() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started.IsZero() {
		c.started = time.Now()
	}
}

// Playing is when the sound began, zero if it never did.
func (c *Claim) Playing() time.Time {
	if c == nil {
		return time.Time{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.started
}

// Quiet is when the queue first ran dry, zero while the sound is still going.
func (c *Claim) Quiet() time.Time {
	if c == nil {
		return time.Time{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finished
}

// Done closes when the sound is over, whether it played out or was silenced.
func (c *Claim) Done() <-chan struct{} {
	if c == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return c.done
}

// Stopped reports whether the claim was taken away rather than finishing.
func (c *Claim) Stopped() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}

// Err is what the errand failed with, if it did.
func (c *Claim) Err() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Claim) Finished() bool {
	select {
	case <-c.Done():
		return true
	default:
		return false
	}
}

// preempt makes way for another sound. A claim that has already played out is left alone: its audio
// has been heard, and draining then would cut off whatever is playing without a claim.
func (c *Claim) preempt(p *Player) {
	if c == nil || c.Finished() {
		return
	}
	c.stop(p)
}

// stop cancels the errand and silences the queue it was filling. Waiting for it to unwind is
// bounded: something that will not return must not stop the next sound from being made.
func (c *Claim) stop(p *Player) {
	if c == nil {
		return
	}

	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()

	c.cancel()
	p.Drain()

	select {
	case <-c.done:
	case <-time.After(time.Second):
		slog.Warn("sound would not stop", "claim", c.name)
	}

	// Whatever it queued on the way out goes too.
	p.Drain()
}

func (c *Claim) mark(quiet time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.finished = quiet
}

func (c *Claim) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}
