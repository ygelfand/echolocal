// Package aec removes the echo of what a device is playing from what its microphone hears, given a
// reference of the playback.
//
// It is a normalised least-mean-squares adaptive filter: it learns the path from the speaker back
// into the microphone and subtracts its prediction. The path has to be reasonably stationary, so the
// microphone it runs on should be a fixed one rather than the output of a beamformer that steers.
//
// The reference must be sample-aligned with the microphone. Hardware that gives a loopback on the
// capture stream satisfies this by construction; a reference taken from the playback side does not,
// and would need its own delay estimation and drift tracking first.
//
// This package is deliberately standalone: nothing outside the standard library, so it can be lifted
// out and used elsewhere.
package aec

import (
	"errors"
	"fmt"
	"math"

	"github.com/ygelfand/echolocal/internal/lib/vec"
)

const (
	// full is int16 full scale. Samples are normalised on the way in so the constants below are
	// absolute levels rather than depending on the caller's units.
	full = 32768

	// quiet is the mean square per tap below which the reference counts as silence, about -60 dBFS.
	// Adapting then would fit the filter to whatever the microphone hears on its own, which is the
	// one thing it must never learn.
	quiet = 1e-6

	// reg keeps the step bounded when the reference is only just above quiet.
	reg = 1e-6

	// erleTau is the averaging length of the reported ERLE, in samples.
	erleTau = 8000

	// refreshEvery recomputes the running reference power from scratch, in multiples of the filter
	// length. Maintaining it incrementally is what keeps a step O(taps) rather than O(taps) twice,
	// but the error in a float32 accumulator grows without a periodic reset.
	refreshEvery = 16
)

// Config is how a Canceller is set up.
type Config struct {
	// Taps is the filter length in samples, so the echo tail it can reach is Taps/rate seconds. It
	// has to cover the delay through the playback and capture hardware as well as the acoustics.
	Taps int

	// Mu is how fast it adapts, between 0 and 2, where larger converges sooner and tracks a changing
	// room better at the cost of more misadjustment. 0.3 is a reasonable default.
	Mu float64
}

// Canceller is one microphone's filter. It is stateful and streaming: frames have to be given to it
// in order, and it is not safe for concurrent use.
type Canceller struct {
	mu   float32
	taps int

	// w is the filter. hist holds the reference twice over, so the Taps most recent samples are
	// always one contiguous slice ending at at+taps.
	w    []float32
	hist []float32
	at   int

	// pow is the sum of squares of the current window, maintained incrementally.
	pow   float32
	since int

	adapting bool

	sumD, sumE float64

	out []int16
}

// New builds a Canceller.
func New(cfg Config) (*Canceller, error) {
	if cfg.Taps <= 0 {
		return nil, fmt.Errorf("aec: taps must be positive, got %d", cfg.Taps)
	}
	if cfg.Mu <= 0 || cfg.Mu >= 2 {
		return nil, fmt.Errorf("aec: mu must be between 0 and 2, got %v", cfg.Mu)
	}

	return &Canceller{
		mu:       float32(cfg.Mu),
		taps:     cfg.Taps,
		w:        make([]float32, cfg.Taps),
		hist:     make([]float32, 2*cfg.Taps),
		adapting: true,
	}, nil
}

// ErrLength is returned when the microphone and reference frames are not the same length.
var ErrLength = errors.New("aec: mic and ref must be the same length")

// Process subtracts the echo from one frame and returns what is left. The returned slice is reused
// on the next call.
func (c *Canceller) Process(mic, ref []int16) ([]int16, error) {
	if len(mic) != len(ref) {
		return nil, ErrLength
	}

	if cap(c.out) < len(mic) {
		c.out = make([]int16, len(mic))
	}
	c.out = c.out[:len(mic)]

	for i := range mic {
		e := c.step(float32(ref[i])/full, float32(mic[i])/full)

		v := e * full
		switch {
		case v > math.MaxInt16:
			v = math.MaxInt16
		case v < math.MinInt16:
			v = math.MinInt16
		}
		c.out[i] = int16(v)
	}
	return c.out, nil
}

// step is one sample: predict the echo, subtract it, and move the filter towards what was left.
func (c *Canceller) step(x, d float32) float32 {
	n := c.taps

	old := c.hist[c.at]
	c.hist[c.at] = x
	c.hist[c.at+n] = x
	c.pow += x*x - old*old

	c.at++
	if c.at == n {
		c.at = 0
	}

	win := c.hist[c.at : c.at+n]

	c.since++
	if c.since >= n*refreshEvery {
		c.since = 0
		c.pow = vec.SumSquares(win)
	}

	// The dot and the AddScaled below are the whole cost of the filter, which is why they go through
	// SIMD. Hand unrolling them in Go measured no faster than plain loops on a Cortex-A53.
	e := d - vec.Dot(c.w, win)

	if c.adapting && c.pow > quiet*float32(n) {
		g := c.mu * e / (c.pow + reg*float32(n))
		vec.AXPY(c.w, g, win)
	}

	c.sumD = c.sumD*(1-1.0/erleTau) + float64(d)*float64(d)
	c.sumE = c.sumE*(1-1.0/erleTau) + float64(e)*float64(e)

	return e
}

// SetAdapting stops or resumes learning while still cancelling with what it has. Freezing is what to
// do when someone is talking over the playback: the filter cannot tell their voice from an echo it
// has predicted badly, and would corrupt itself trying to cancel it.
func (c *Canceller) SetAdapting(on bool) { c.adapting = on }

// Adapting reports whether it is learning.
func (c *Canceller) Adapting() bool { return c.adapting }

// ERLE is the echo return loss enhancement over roughly the last erleTau samples: how much quieter
// the output is than the microphone was. It measures whatever the filter removed, so it is only
// meaningful while there is playback to remove.
func (c *Canceller) ERLE() float64 {
	if c.sumE <= 0 || c.sumD <= 0 {
		return 0
	}
	return 10 * math.Log10(c.sumD/c.sumE)
}

// Reset forgets the room. Worth doing when playback stops and starts again somewhere acoustically
// different, and necessary if the microphone it is running on changes.
func (c *Canceller) Reset() {
	for i := range c.w {
		c.w[i] = 0
	}
	for i := range c.hist {
		c.hist[i] = 0
	}
	c.at, c.pow, c.since = 0, 0, 0
	c.sumD, c.sumE = 0, 0
}

// Taps is the filter length it was built with.
func (c *Canceller) Taps() int { return c.taps }
