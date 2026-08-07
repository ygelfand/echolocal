package sendspin

import (
	"testing"

	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

// anchored fixes the frame/time origin directly, so the tests are about placement rather than about
// what the clock filter happened to converge to.
func anchored(t *testing.T) *out {
	t.Helper()

	o := newOut(nil)
	o.ready = true
	o.anchored = true
	o.frame = 1000
	o.at = 0
	o.played = 1000
	return o
}

func frames(n int) []int16 { return make([]int16, n*speaker.Channels) }

func tone(n int, v int16) []int16 {
	s := frames(n)
	for i := range s {
		s[i] = v
	}
	return s
}

// microsFor is the server timestamp that lands audio n frames after the anchor.
func microsFor(n int64) int64 { return n * 1e6 / speaker.Rate }

func rendered(o *out, from uint64, n int) []int16 {
	buf := frames(n)
	o.Render(from, buf)
	return buf
}

// The whole point: the same timestamp lands on the same frame no matter when it arrived.
func TestAChunkLandsOnTheFrameItsTimestampAsksFor(t *testing.T) {
	o := anchored(t)
	o.write(microsFor(480), tone(10, 500))

	if got := rendered(o, 1000, 10)[0]; got != 0 {
		t.Errorf("at the anchor = %d, want silence, the audio is 480 frames later", got)
	}
	if got := rendered(o, 1480, 10)[0]; got != 500 {
		t.Errorf("at the placed frame = %d, want 500", got)
	}
}

// Arriving out of order must not matter, which is what makes a jitter burst recoverable.
func TestOrderOfArrivalDoesNotMoveAudio(t *testing.T) {
	o := anchored(t)
	o.write(microsFor(480), tone(10, 700))
	o.write(microsFor(0), tone(10, 300))

	if got := rendered(o, 1000, 10)[0]; got != 300 {
		t.Errorf("first chunk = %d, want 300", got)
	}
	if got := rendered(o, 1480, 10)[0]; got != 700 {
		t.Errorf("second chunk = %d, want 700", got)
	}
}

// A gap between chunks is silence at the frames nobody filled, not audio pulled earlier.
func TestAGapPlaysAsSilenceRatherThanShiftingWhatFollows(t *testing.T) {
	o := anchored(t)
	o.write(microsFor(0), tone(10, 400))
	o.write(microsFor(2000), tone(10, 900))

	if got := rendered(o, 1010, 10)[0]; got != 0 {
		t.Errorf("in the gap = %d, want silence", got)
	}
	if got := rendered(o, 3000, 10)[0]; got != 900 {
		t.Errorf("after the gap = %d, want 900", got)
	}
}

// Audio whose frame has already gone is dropped, not played late. Playing it is what leaves a room
// permanently behind the others.
func TestAudioThatArrivesAfterItsFrameIsDropped(t *testing.T) {
	o := anchored(t)
	rendered(o, 5000, 10)

	o.write(microsFor(0), tone(10, 600))

	if o.late.Load() != 1 {
		t.Errorf("late = %d, want 1", o.late.Load())
	}
	if got := rendered(o, 5010, 10)[0]; got != 0 {
		t.Errorf("played = %d, want silence", got)
	}
}

// Only the part of a chunk that is still due gets played.
func TestAPartlyLateChunkKeepsTheFramesStillToCome(t *testing.T) {
	o := anchored(t)
	rendered(o, 1005, 1)

	o.write(microsFor(0), tone(10, 800))

	if got := rendered(o, 1005, 5)[0]; got != 800 {
		t.Errorf("remainder = %d, want 800", got)
	}
}

// An underrun re-asks for the same frames, so dropping what has passed cannot be a consume.
func TestRenderingTheSameFrameTwiceGivesTheSameAudio(t *testing.T) {
	o := anchored(t)
	o.write(microsFor(0), tone(10, 250))

	first := rendered(o, 1000, 10)[0]
	second := rendered(o, 1000, 10)[0]

	if first != 250 || second != 250 {
		t.Errorf("got %d then %d, want 250 both times", first, second)
	}
}

// A timestamp we cannot believe must not turn into an allocation.
func TestAbsurdlyDistantAudioIsRefused(t *testing.T) {
	o := anchored(t)
	o.write(microsFor(holdMax+speaker.Rate), tone(10, 100))

	if o.dropped.Load() != 1 {
		t.Errorf("dropped = %d, want 1", o.dropped.Load())
	}
	if len(o.pcm) != 0 {
		t.Errorf("held %d samples, want none", len(o.pcm))
	}
}

func TestDuckingAppliesAsFramesAreRendered(t *testing.T) {
	o := anchored(t)
	o.write(microsFor(0), tone(10, 1000))
	o.gain = 0.5

	if got := rendered(o, 1000, 10)[0]; got != 500 {
		t.Errorf("ducked = %d, want 500", got)
	}
}

func TestSuspendedPlaysNothing(t *testing.T) {
	o := anchored(t)
	o.write(microsFor(0), tone(10, 1000))
	o.held = true

	if got := rendered(o, 1000, 10)[0]; got != 0 {
		t.Errorf("suspended = %d, want silence", got)
	}
}

// A flush ends the timeline: what comes next anchors again rather than against the old stream.
func TestFlushDropsTheAnchor(t *testing.T) {
	o := anchored(t)
	o.write(microsFor(0), tone(10, 100))
	o.flush()

	if o.anchored {
		t.Error("still anchored after a flush")
	}
	if len(o.pcm) != 0 {
		t.Errorf("held %d samples, want none", len(o.pcm))
	}
}
