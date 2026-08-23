package led

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func testRing(t *testing.T) *Ring {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "frame"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return &Ring{Path: dir}
}

func TestSplashTimesOutAndTurnsRingOff(t *testing.T) {
	r := testRing(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := splash(ctx, r, func() bool { return false }, 2*FrameInterval,
		2*FrameInterval, 2*FrameInterval); err != nil {
		t.Fatal(err)
	}
	frame, err := r.Frame()
	if err != nil {
		t.Fatal(err)
	}
	for i, value := range frame {
		if value != 0 {
			t.Fatalf("channel %d is %d after timed-out splash, want off", i, value)
		}
	}
}

func TestUntilReportsReadySeparatelyFromTimeout(t *testing.T) {
	r := testRing(t)
	var ready atomic.Bool
	go func() {
		time.Sleep(FrameInterval)
		ready.Store(true)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*FrameInterval)
	defer cancel()
	confirmed, err := until(ctx, r, walk(HomeAssistant), ready.Load)
	if err != nil {
		t.Fatal(err)
	}
	if !confirmed {
		t.Fatal("until reported timeout after ready became true")
	}
}

// The boot walk has to advance evenly all the way round, including across the wrap. An uneven step
// there reads as the light jumping backwards.
func TestWalkStepsEvenlyAroundTheRing(t *testing.T) {
	frame := walk(HomeAssistant)

	var lit []int
	for step := range 14 {
		f := frame(time.Duration(step)*WalkStep + WalkStep/2)

		on := -1
		for i, c := range f {
			if c == (Color{}) {
				continue
			}
			if on >= 0 {
				t.Fatalf("step %d lit segments %d and %d, want one", step, on, i)
			}
			on = i
		}
		if on < 0 {
			t.Fatalf("step %d lit nothing", step)
		}
		lit = append(lit, on)
	}
	t.Logf("segments: %v", lit)

	for i := 1; i < len(lit); i++ {
		// Clockwise by two segments every time, wrapping.
		if want := (lit[i-1] + 2) % Segments; lit[i] != want {
			t.Errorf("step %d lit segment %d, want %d (previous %d)", i, lit[i], want, lit[i-1])
		}
	}
}
