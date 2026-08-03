package mic

import (
	"log/slog"
	"sync/atomic"

	"github.com/ygelfand/echolocal/internal/lib/aec"
	"github.com/ygelfand/echolocal/internal/lib/audio"
)

// cancelTaps is how far the filter reaches, in samples, so 64 ms of tail. Not a setting: measured on
// this enclosure, cancellation rises with every tap available — 16.5 dB at 256, 18.6 at 512, 23.0 at
// 1024, 27.2 at 2048 — so there is no room-dependent best value to look for, only what the device can
// afford. This costs 6.4% of a core, and only while something is playing.
const cancelTaps = 1024

// cancelMu is how fast the filter adapts. Fast enough to converge inside the first second of a reply,
// which matters because a reply is all the time there is.
const cancelMu = 0.5

// refQuiet is the mean square per sample, at int16 scale, below which the loopback counts as silence.
// About -60 dBFS. Below it there is no echo to remove, so the filter is skipped entirely and the frame
// costs nothing — which is what keeps this free on an idle device.
const refQuiet = 1e-6 * 32768 * 32768

// refHold is how long the filter keeps running after the loopback goes quiet, in samples.
//
// Speech is full of gaps, and without this the gate flaps between every word. That is not only untidy:
// the room is still ringing with the echo of the word that just played, and the measured tail here runs
// past 100 ms, so disengaging on the gap stops cancelling exactly as that tail arrives. 250 ms covers
// it with room to spare.
const refHold = Rate / 4

// canceller subtracts the playback loopback from one fixed microphone.
//
// It runs on the center microphone rather than the mix, and replaces the mix while it runs. Two reasons,
// and simplicity is the lesser of them: the filter has to learn one acoustic path, so the microphone it
// reads cannot move — and the beamformer steers at the loudest sound, which during playback is our own
// speaker. Steering into the echo is the opposite of useful when the echo is what is being removed.
type canceller struct {
	filter *aec.Canceller

	// ref and mono are reused every frame; decoding into them keeps the audio path free of allocation.
	ref  []int16
	mono []int16

	// erle is published for diagnostics, as thousandths of a dB so it fits an integer. best is the most
	// it reached during the current run: ERLE is averaged over the last half second, so reading it as
	// playback ends catches the reference fading out and says the filter did worse than it did.
	erle atomic.Int64
	best atomic.Int64

	// active says whether the last frame had playback in it, which is when any of this happened.
	active atomic.Bool

	// hold counts down the samples still to filter after the loopback went quiet. Reader-only.
	hold int
}

func newCanceller() *canceller {
	f, err := aec.New(aec.Config{Taps: cancelTaps, Mu: cancelMu})
	if err != nil {
		// Only a bad Taps or Mu reaches this, and both are constants above.
		slog.Error("echo cancellation unavailable", "err", err)
		return nil
	}
	return &canceller{filter: f}
}

// apply returns the center microphone with the echo removed, or nil when there is nothing playing and
// the caller should use the mix it already has.
func (c *canceller) apply(raw []byte, mics [][]int16) []int16 {
	n := len(mics[CenterMic])
	if cap(c.ref) < n {
		c.ref = make([]int16, n)
	}
	c.ref = c.ref[:n]
	referenceInto(raw, c.ref)

	switch {
	case playing(c.ref):
		c.hold = refHold
	case c.hold > 0:
		// Still inside the tail of what just played.
		c.hold -= n
	default:
		// Nothing to cancel. The filter keeps what it learned: the room has not changed just because
		// the reply ended, so the next one starts converged rather than from nothing.
		if c.active.Swap(false) {
			slog.Info("echo cancellation idle",
				"best_db", float64(c.best.Load())/1000, "last_db", float64(c.erle.Load())/1000)
			c.erle.Store(0)
			c.best.Store(0)
		}
		return nil
	}

	if !c.active.Swap(true) {
		slog.Info("echo cancellation running", "taps", cancelTaps)
	}

	out, err := c.filter.Process(mics[CenterMic], c.ref)
	if err != nil {
		slog.Error("echo cancellation failed", "err", err)
		return nil
	}
	erle := int64(c.filter.ERLE() * 1000)
	c.erle.Store(erle)
	if erle > c.best.Load() {
		c.best.Store(erle)
	}

	// Process reuses its buffer and listeners keep what they are handed, so this is copied out.
	if cap(c.mono) < len(out) {
		c.mono = make([]int16, len(out))
	}
	c.mono = c.mono[:len(out)]
	copy(c.mono, out)
	return c.mono
}

// playing reports whether the loopback carries anything.
func playing(ref []int16) bool {
	var sum float64
	for _, v := range ref {
		sum += float64(v) * float64(v)
	}
	return len(ref) > 0 && sum/float64(len(ref)) > refQuiet
}

// referenceInto decodes ch7, the left half of the playback loopback, into dst. The speaker is mono into
// one driver, so ch8 carries the same programme and either alone is the reference.
func referenceInto(raw []byte, dst []int16) {
	const frameBytes = Channels * Bits / 8

	frames := min(len(raw)/frameBytes, len(dst))
	for f := range frames {
		o := f*frameBytes + Mics*3
		dst[f] = int16(audio.DecodeS24LE3(raw[o:o+3]) >> 8)
	}
	for f := frames; f < len(dst); f++ {
		dst[f] = 0
	}
}

// Cancelling reports whether the canceller is currently running, which it does only while the loopback
// is carrying audio.
func (s *Source) Cancelling() bool {
	return s.cancel != nil && s.cancel.active.Load()
}

// ERLE is how much echo the canceller is removing, in dB, or zero when it is not running.
func (s *Source) ERLE() float64 {
	if s.cancel == nil {
		return 0
	}
	return float64(s.cancel.erle.Load()) / 1000
}

// SetCancelling turns echo cancellation on or off. Off is the same signal path the device had before it
// existed, which is what makes it worth having as a switch: it is the comparison.
func (s *Source) SetCancelling(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelling = on
}

// SetAdapting stops or resumes the canceller learning, while it goes on cancelling with what it has.
//
// Turn it off while somebody is being listened to. The filter cannot tell a voice it was never given a
// reference for from an echo it predicted badly, so it treats the voice as its own error and fits itself
// to it — at exactly the moment cancelling matters. What it already learned stays correct: the room did
// not change because somebody spoke.
func (s *Source) SetAdapting(on bool) {
	if s.cancel != nil {
		s.cancel.filter.SetAdapting(on)
	}
}
