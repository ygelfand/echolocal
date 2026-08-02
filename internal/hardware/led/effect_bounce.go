package led

import (
	"math"
	"time"
)

// bounce drops a ball, which keeps a fraction of its height each time it lands until there is nothing
// left to bounce and it is dropped again.
//
// Each bounce is a parabola, so the ball is quick through the bottom and slow at the top, which is
// the part that reads as weight. A bounce's duration goes with the square root of its height, so the
// bounces shorten as they shrink on their own — nothing here has to say how long each one lasts. The
// whole sequence is worked out once and looked up, which keeps the frame function free of state and
// so still reversible and still restartable.
func bounce(p Palette) Frame {
	const (
		first = 900 * time.Millisecond
		keeps = 0.55

		// Below this there is no bounce left to see, and the ring rests before the next drop.
		spent = 0.04
		rest  = 700 * time.Millisecond
	)

	type arc struct {
		at     time.Duration
		d      time.Duration
		height float64
	}

	var arcs []arc
	var cycle time.Duration
	for h := 1.0; h > spent; h *= keeps {
		d := time.Duration(float64(first) * math.Sqrt(h))
		arcs = append(arcs, arc{at: cycle, d: d, height: h})
		cycle += d
	}
	cycle += rest

	return func(elapsed time.Duration) []Color {
		t := elapsed % cycle

		var at float64
		for _, a := range arcs {
			if t < a.at || t >= a.at+a.d {
				continue
			}
			// A parabola through the two ends of the bounce, peaking at its height in the middle.
			x := 2*float64(t-a.at)/float64(a.d) - 1
			at = a.height * (1 - x*x) * (Segments - 1)
			break
		}

		out := make([]Color, Segments)
		dot(out, at, p.Along(at/(Segments-1)), 1.5)
		return out
	}
}
