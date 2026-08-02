package led

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// ringInDir gives a Ring writing to a temporary directory, so the driver can be exercised without
// hardware. Only the frame attribute is read back.
func ringInDir(t *testing.T) *Ring {
	t.Helper()

	dir := t.TempDir()
	for _, name := range []string{"frame", "leds_current", "boot_animation"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatalf("preparing %s: %v", name, err)
		}
	}
	return &Ring{Path: dir}
}

// shown is the color of segment 0 as it reached the hardware.
//
// A frame write truncates the file and writes it again, so reading while an animation is running can
// catch it empty. That is an artefact of a file standing in for the hardware — the real attribute takes
// a whole frame at once — so a short read is retried rather than being a failure.
func shown(t *testing.T, r *Ring) Color {
	t.Helper()

	for try := range 20 {
		if try > 0 {
			time.Sleep(2 * time.Millisecond)
		}

		vals, err := r.Frame()
		if err != nil {
			t.Fatalf("reading frame: %v", err)
		}
		if len(vals) >= 3 {
			return Color{R: vals[0], G: vals[1], B: vals[2]}
		}
	}

	t.Fatal("the frame stayed empty")
	return Color{}
}

// settle waits for the render loop to write. The driver renders as soon as it is woken, so this only
// has to outlast a scheduling hop. It cannot simply be made generous: a timed claim expires on its own
// deadline, and a test watching one expire needs to look before it does.
func settle() { time.Sleep(40 * time.Millisecond) }

// waitFor is settle for something that has to change: it polls until the ring shows what is expected,
// which is what a test wants when a scheduling hop under load is the only thing in the way. A fixed
// sleep either flakes or is slower than every run needs to be.
func waitFor(t *testing.T, r *Ring, want Color) Color {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	var got Color
	for time.Now().Before(deadline) {
		if got = shown(t, r); got == want {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	return got
}

func solid(c Color) []Color {
	out := make([]Color, Segments)
	for i := range out {
		out[i] = c
	}
	return out
}

var (
	red   = Color{R: 0xC0}
	green = Color{G: 0xC0}
	blue  = Color{B: 0xC0}
)

func running(t *testing.T) *Driver {
	t.Helper()

	d := NewDriver(ringInDir(t))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() { _ = d.Run(ctx) }()
	return d
}

// A higher priority wins however the claims were taken, and releasing it reveals what was underneath
// rather than leaving the ring blank or stuck.
func TestPriorityWinsAndUncovers(t *testing.T) {
	d := running(t)

	base := d.Claim(PriorityBase)
	base.Paint(solid(green))
	settle()
	if got := shown(t, d.ring); got != green {
		t.Fatalf("base alone shows %+v, want green", got)
	}

	turn := d.Claim(PriorityTurn)
	turn.Paint(solid(blue))
	settle()
	if got := shown(t, d.ring); got != blue {
		t.Fatalf("with a turn claim the ring shows %+v, want blue", got)
	}

	// The failure has to win even though the turn claimed the ring first and is still holding it.
	trouble := d.Claim(PriorityTrouble)
	trouble.Paint(solid(red))
	settle()
	if got := shown(t, d.ring); got != red {
		t.Fatalf("with trouble up the ring shows %+v, want red", got)
	}

	// This is the case that was broken before: the turn ending must not take the failure away.
	turn.Release()
	settle()
	if got := shown(t, d.ring); got != red {
		t.Fatalf("after the turn ended the ring shows %+v, want red still", got)
	}

	trouble.Release()
	settle()
	if got := shown(t, d.ring); got != green {
		t.Fatalf("after trouble released the ring shows %+v, want the base back", got)
	}
}

// A claim taken but never given anything to say must not blank the ring.
func TestEmptyClaimShowsNothing(t *testing.T) {
	d := running(t)

	base := d.Claim(PriorityBase)
	base.Paint(solid(green))
	settle()

	_ = d.Claim(PriorityBoot)
	settle()

	if got := shown(t, d.ring); got != green {
		t.Errorf("an empty claim changed the ring to %+v", got)
	}
}

// A timed claim releases itself.
func TestTimedClaimExpires(t *testing.T) {
	d := running(t)

	base := d.Claim(PriorityBase)
	base.Paint(solid(green))

	notice := d.Claim(PriorityNotice)
	notice.PaintFor(solid(blue), 80*time.Millisecond)
	settle()
	if got := shown(t, d.ring); got != blue {
		t.Fatalf("the notice shows %+v, want blue", got)
	}

	time.Sleep(140 * time.Millisecond)
	if got := shown(t, d.ring); got != green {
		t.Errorf("after the notice expired the ring shows %+v, want the base back", got)
	}
}

// Among equal priorities the most recent claim wins, so two things of the same kind do not fight.
func TestLatestOfEqualPriorityWins(t *testing.T) {
	d := running(t)

	first := d.Claim(PriorityNotice)
	first.Paint(solid(green))
	second := d.Claim(PriorityNotice)
	second.Paint(solid(blue))
	settle()

	if got := shown(t, d.ring); got != blue {
		t.Errorf("the ring shows %+v, want the later claim", got)
	}

	second.Release()
	settle()
	if got := shown(t, d.ring); got != green {
		t.Errorf("after the later claim released the ring shows %+v, want the earlier one", got)
	}
}

// Changing content on the claim that is showing takes effect without re-claiming.
func TestContentSwap(t *testing.T) {
	d := running(t)

	c := d.Claim(PriorityTurn)
	c.Paint(solid(green))
	settle()

	c.Paint(solid(red))
	settle()
	if got := shown(t, d.ring); got != red {
		t.Errorf("after a swap the ring shows %+v, want red", got)
	}

	c.Clear()
	settle()
	if got := shown(t, d.ring); got != (Color{}) {
		t.Errorf("after clearing the only claim the ring shows %+v, want blank", got)
	}
}

// A claim underneath being repainted must not restart the animation on top of it. This is why the
// boot walk used to begin again whenever Home Assistant reported anything.
func TestLowerClaimDoesNotRestartTheTop(t *testing.T) {
	d := running(t)

	base := d.Claim(PriorityBase)
	base.Paint(solid(green))

	// Counted from the driver's goroutine and read from this one.
	var starts atomic.Int32
	boot := d.Claim(PriorityBoot)
	boot.Show(Content{Animate: func(ctx context.Context, r *Ring) error {
		starts.Add(1)
		if err := r.SetSegments(solid(blue)); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}})
	settle()

	if n := starts.Load(); n != 1 {
		t.Fatalf("the animation started %d times before anything changed", n)
	}

	// Anything at all happening underneath.
	for range 3 {
		base.Paint(solid(red))
		settle()
		base.Paint(solid(green))
		settle()
	}

	if n := starts.Load(); n != 1 {
		t.Errorf("the animation restarted: %d starts", n)
	}
	if got := shown(t, d.ring); got != blue {
		t.Errorf("the ring shows %+v, want the boot animation still", got)
	}

	// Changing the top claim itself does restart it, which is the point of the distinction.
	boot.Show(Content{Frame: solid(red)})
	settle()
	if got := shown(t, d.ring); got != red {
		t.Errorf("after changing the top claim the ring shows %+v, want red", got)
	}
}

// Using a released claim is harmless, since a holder may outlive its turn on the ring.
func TestReleasedClaimIsInert(t *testing.T) {
	d := running(t)

	base := d.Claim(PriorityBase)
	base.Paint(solid(green))

	gone := d.Claim(PriorityTrouble)
	gone.Paint(solid(red))
	settle()
	gone.Release()
	settle()

	gone.Paint(solid(blue))
	settle()
	if got := shown(t, d.ring); got != green {
		t.Errorf("a released claim painted %+v", got)
	}
}

// A room reaction is dark until something happens, so while it is dark the light underneath has to show
// through: choosing to follow the room should not mean giving up the ring's own colour for the evenings
// nobody says anything.
func TestARoomReactionShowsTheLightThroughItsSilence(t *testing.T) {
	d := running(t)

	base := d.Claim(PriorityBase)
	base.Paint(solid(green))

	// A quiet room, then a loud one, from the same source the driver reads every frame.
	var loud atomic.Bool
	room := d.Claim(PriorityRoom)
	room.React(EffectRoomGlow, blue, Room{Level: func() float64 {
		if loud.Load() {
			return 1
		}
		return 0
	}})
	settle()

	if got := shown(t, d.ring); got != green {
		t.Errorf("a silent room shows %+v, want the light underneath", got)
	}

	loud.Store(true)
	if got := waitFor(t, d.ring, blue); got != blue {
		t.Errorf("a loud room shows %+v, want the reaction", got)
	}

	// And back, without anything being claimed or released in between.
	loud.Store(false)
	if got := waitFor(t, d.ring, green); got != green {
		t.Errorf("once the room went quiet the ring shows %+v, want the light again", got)
	}
}

// Only something reading the room may be seen through. An animation that goes dark on purpose — a
// heartbeat between thumps, an alert between pulses — owns the ring while it does, or the layer beneath
// would strobe through every gap.
func TestADarkAnimationStillOwnsTheRing(t *testing.T) {
	d := running(t)

	base := d.Claim(PriorityBase)
	base.Paint(solid(green))

	// Heartbeat is entirely dark for most of its period, which is what makes it the case worth testing.
	turn := d.Claim(PriorityTurn)
	turn.Play(EffectHeartbeat, red)

	// Waited for rather than settled: until the animation's first write lands, the frame still holds
	// what the claim underneath left there, and reading that proves nothing.
	for shown(t, d.ring) == green {
		time.Sleep(FrameInterval)
	}

	for range 30 {
		if got := shown(t, d.ring); got == green {
			t.Fatal("the light underneath showed through a resting heartbeat")
		}
		time.Sleep(FrameInterval)
	}
}
