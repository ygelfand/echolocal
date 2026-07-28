package led

import (
	"testing"
	"time"
)

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
