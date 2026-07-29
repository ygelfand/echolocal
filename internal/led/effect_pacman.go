package led

import (
	"math"
	"time"
)

// pacman eats his way round the ring and starts again. What makes him recognisable is not the shape
// of him, which twelve segments cannot draw, but that the ring ahead is dotted and the ring behind is
// dark: something is being consumed rather than merely passing by.
func pacman(p Palette) Frame {
	const (
		lap = 3600 * time.Millisecond

		// The chomp. Fast enough to read as a mouth, not so fast it looks like a fault.
		chomp = 220 * time.Millisecond
	)

	return func(elapsed time.Duration) []Color {
		at := float64(elapsed%lap) / float64(lap) * Segments

		out := make([]Color, Segments)

		// Pellets on every other segment he has not reached yet. Every other, because a pellet on
		// every one leaves nothing between them and reads as a lit ring.
		for i := range Segments {
			if i%2 == 0 && float64(i) > at {
				out[i] = p.Nth(1)
			}
		}

		open := math.Abs(math.Sin(math.Pi * float64(elapsed%chomp) / float64(chomp)))
		dot(out, at, scale(p.Nth(0), 0.55+0.45*(1-open)), 1.3)
		return out
	}
}
