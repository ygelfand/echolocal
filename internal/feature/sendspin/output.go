// Package sendspin plays a room's part of a synchronized stream.
//
// Wire format and clock filter come from sendspin-go's protocol and sync packages. Everything past the
// socket is echod's: the listener a server dials in to, the decoders, and the speaker.
package sendspin

import (
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	ssync "github.com/Sendspin/sendspin-go/pkg/sync"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

// holdMax bounds what is held ahead. The server keeps within the buffer capacity we advertised, so
// reaching this means a timestamp we cannot believe rather than a server sending too much.
const holdMax = 60 * speaker.Rate

// Drift correction. The anchor maps server time to output frames at the nominal rate, but the speaker's
// clock is not the server's: measured on device, one Dot ran about 200 ppm fast and pulled ahead of
// another by 12 ms a minute. So every render period the frame being heard is compared with the frame
// the server clock says should be, the error is smoothed, and one frame is dropped or repeated per
// period while the smoothed error is outside the band. A frame is 21 us; the spec's own suggestion,
// and inaudible at the handful per second a real drift needs.
//
// driftGain is the smoothing: at 47 periods a second, a time constant of about a second, enough to
// take the scheduling jitter out of when a period happens to be rendered. driftBand is half a
// millisecond either side, inside the spec's 1 ms floor with room for the noise that remains.
//
// An error past snapBand is not drift but a misplaced anchor, most often a period's worth of phase
// between the write counter and the card at the moment the stream started, or an underrun that moved
// the counter on without playing anything. That is put right in one step of silence or one skip, which
// the spec allows on a start, and the fine correction takes it from there.
//
// tailFrames is the hardware tail in frames: the write counter is that far ahead of what is heard, and
// the anchor was laid in terms of what is heard.
const (
	driftGain  = 0.02
	driftBand  = speaker.Rate / 2000
	snapBand   = speaker.Rate / 100
	tailFrames = int64(speaker.HardwareTail * speaker.Rate / time.Second)

	// maxSnap is the largest correction that can be one, above which the anchor predates a change of
	// clock rather than having drifted.
	maxSnap = 2 * speaker.Rate
)

// out places this room's audio by output frame index. Arrival order cannot line two rooms up: a burst
// of jitter on one of them shifts it against the other for good, because nothing says where the audio
// was meant to go. The server's timestamps say, so they decide.
type out struct {
	p     *speaker.Player
	clock *ssync.ClockSync

	mu    sync.Mutex
	ready bool
	held  bool
	gain  float32

	// pcm holds frames from base onward. played is where the card has got to, which is what decides
	// whether a chunk is late — base only says what we happen to be holding.
	base   uint64
	played uint64
	pcm    []int16

	// The frame that carries server time at. Fixed once per stream: recomputing it per chunk would
	// feed the sampling jitter of "what is playing now" straight back into where audio lands. Drift
	// correction nudges frame by whole frames instead, in step with the audio it moves.
	anchored bool
	frame    uint64
	at       int64

	// drift is the smoothed error between the frame being heard and the frame the server clock wants,
	// in frames, positive when the room is playing early. corrected counts frames repeated (positive)
	// or dropped (negative) to hold it.
	drift     float64
	corrected int64

	// nextReport is the frame the next correction line is due at, one a second.
	nextReport uint64

	// now stands in for the clock, so a test can hold the server's time still.
	now func() int64

	late    atomic.Int64
	dropped atomic.Int64
}

var (
	_ speaker.Producer = (*out)(nil)
	_ speaker.Source   = (*out)(nil)
)

func newOut(p *speaker.Player) *out { return &out{p: p, gain: 1} }

// use points the renderer at this session's clock. One server at a time, so it changes only between
// sessions.
func (o *out) use(clock *ssync.ClockSync) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.clock = clock
}

// open refuses anything the speaker cannot play, rather than playing it at the wrong speed.
func (o *out) open(sampleRate, channels, bitDepth int) error {
	if sampleRate != speaker.Rate || channels != speaker.Channels || bitDepth != speaker.Bits {
		return fmt.Errorf("sendspin: cannot play %d Hz/%dch/%d-bit, the speaker is %d/%d/%d",
			sampleRate, channels, bitDepth, speaker.Rate, speaker.Channels, speaker.Bits)
	}

	o.mu.Lock()
	o.ready = true
	o.mu.Unlock()

	o.p.Attach(o)
	return nil
}

func (o *out) close() {
	o.p.Attach(nil)

	o.mu.Lock()
	defer o.mu.Unlock()
	o.ready = false
	o.reset()
}

// write places a decoded chunk at the frame its timestamp asks for.
func (o *out) write(at int64, samples []int16) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.ready {
		o.dropped.Add(1)
		return
	}

	frame := o.frameFor(at)
	if frame > o.played && frame-o.played > holdMax {
		o.dropped.Add(1)
		return
	}

	// A chunk that reaches us after its frames have played is late: whatever is left of it still
	// belongs where it was meant to go, and the rest is gone. Playing it anyway is what leaves a room
	// behind for good.
	span := uint64(len(samples) / speaker.Channels)
	if frame+span <= o.played {
		o.late.Add(1)
		return
	}
	if frame < o.played {
		samples = samples[(o.played-frame)*speaker.Channels:]
		frame = o.played
	}

	if len(o.pcm) == 0 {
		o.base = frame
	}
	if frame < o.base {
		o.pcm = append(make([]int16, (o.base-frame)*speaker.Channels), o.pcm...)
		o.base = frame
	}

	off := int(frame-o.base) * speaker.Channels
	if need := off + len(samples); need > len(o.pcm) {
		o.pcm = append(o.pcm, make([]int16, need-len(o.pcm))...)
	}
	copy(o.pcm[off:], samples)
}

// frameFor converts a server timestamp to an output frame. Wants mu.
func (o *out) frameFor(at int64) uint64 {
	if o.anchored {
		return uint64(int64(o.frame) + (at-o.at)*speaker.Rate/1e6)
	}

	// Frame Written() is going to the card now and is heard a hardware tail later, so this is where
	// the server's intended moment falls. The tail is a constant and the same on every Dot, so what it
	// costs in absolute accuracy it does not cost in lining rooms up.
	ahead := time.Until(o.clock.ServerToLocalTime(at)) - speaker.HardwareTail
	o.frame = o.p.Written() + uint64(max(0, ahead.Seconds()*speaker.Rate))
	o.at = at
	o.anchored = true
	o.played = o.frame

	// Both conversions of the same timestamp: ahead_ms goes through local time, lead_ms stays in the
	// server's frame, and correct() holds the room to the second. They should agree.
	slog.Info("sendspin anchored", "frame", o.frame, "ahead_ms", ahead.Milliseconds(),
		"lead_ms", (at-o.clock.ServerMicrosNow())/1000, "written", o.p.Written(),
		"quality", o.clock.CheckQuality())
	return o.frame
}

// Render is the speaker asking what this room plays next.
func (o *out) Render(from uint64, buf []int16) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if from > o.played {
		o.played = from
	}

	// The card has passed these, and an underrun can ask for the same frames twice, so dropping has to
	// be idempotent rather than a consume.
	if from > o.base && len(o.pcm) > 0 {
		if gone := int((from - o.base) * speaker.Channels); gone >= len(o.pcm) {
			o.pcm = o.pcm[:0]
		} else {
			o.pcm = append(o.pcm[:0], o.pcm[gone:]...)
		}
	}
	if from > o.base {
		o.base = from
	}

	if o.held || !o.ready || from < o.base {
		return
	}
	o.correct(from)
	for i := range min(len(buf), len(o.pcm)) {
		buf[i] = scale(o.pcm[i], o.gain)
	}
}

// correct holds the room to the server clock. Wants mu, and pcm's head at from.
func (o *out) correct(from uint64) {
	if !o.anchored || len(o.pcm) < 2*speaker.Channels {
		return
	}
	now := o.now
	if now == nil {
		if o.clock == nil {
			return
		}
		// Without a sync the clock reads the server's timestamps as local ones, decades out, and every
		// period would re-anchor against an answer it does not have. Hold what is queued: the anchor is
		// wrong until a sync lands, and the first correction after one puts it right in a single step.
		if o.clock.CheckQuality() == ssync.QualityLost {
			return
		}
		now = o.clock.ServerMicrosNow
	}

	// The frame the server clock says should be heard now, against the one that is: the frame being
	// rendered less the tail it has yet to travel.
	want := int64(o.frame) + (now()-o.at)*speaker.Rate/1e6

	// The loop has the hardware tail in it: frames already handed to the card still play at the old
	// alignment, so a correction at full gain overshoots and the next one swings back. driftGain is
	// what damps that, and a snap is bounded by it for the same reason.
	off := float64(int64(from) - tailFrames - want)
	o.drift += (off - o.drift) * driftGain

	// What the correction is actually looking at, once a second: off should sit near zero and stay
	// there. Where it does not, these say whether the room, the anchor or the server clock is moving.
	if from >= o.nextReport {
		o.nextReport = from + speaker.Rate
		slog.Info("sendspin correction", "off_ms", int64(off)*1000/speaker.Rate,
			"drift_ms", int64(o.drift)*1000/speaker.Rate, "from", from, "want", want,
			"anchor_frame", o.frame, "corrected", o.corrected,
			"queued_ms", len(o.pcm)/speaker.Channels*1000/speaker.Rate)
	}

	// Re-anchor rather than snap: the next chunk lays one against the clock the server is using now.
	if off > maxSnap || off < -maxSnap {
		slog.Warn("sendspin re-anchoring", "off_s", int64(off)/speaker.Rate)
		o.anchored = false
		o.drift = 0
		return
	}

	switch {
	case o.drift > snapBand:
		n := int(o.drift)
		o.pcm = append(make([]int16, n*speaker.Channels), o.pcm...)
		o.frame += uint64(n)
		o.corrected += int64(n)
		o.drift = 0
		slog.Info("sendspin snapped later", "ms", n*1000/speaker.Rate)
	case o.drift < -snapBand:
		n := min(int(-o.drift), len(o.pcm)/speaker.Channels-1)
		o.pcm = append(o.pcm[:0], o.pcm[n*speaker.Channels:]...)
		o.frame -= uint64(n)
		o.corrected -= int64(n)
		o.drift = 0
		slog.Info("sendspin snapped earlier", "ms", n*1000/speaker.Rate)
	case o.drift > driftBand:
		// Early: say the first frame twice, and move the anchor with it so what arrives next lands in
		// step with what is already queued.
		o.pcm = append(o.pcm, o.pcm[:speaker.Channels]...)
		copy(o.pcm[speaker.Channels:], o.pcm[:len(o.pcm)-speaker.Channels])
		o.frame++
		o.drift--
		o.corrected++
	case o.drift < -driftBand:
		// Late: skip the first frame.
		n := copy(o.pcm, o.pcm[speaker.Channels:])
		o.pcm = o.pcm[:n]
		o.frame--
		o.drift++
		o.corrected--
	}
}

// drifting reports the smoothed error in frames and the frames corrected so far, for the log.
func (o *out) drifting() (drift float64, corrected int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.drift, o.corrected
}

// flush drops what has not been heard yet, and the anchor with it: what comes next is a new timeline.
func (o *out) flush() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reset()
}

// reset wants mu.
func (o *out) reset() {
	o.pcm = o.pcm[:0]
	o.base = 0
	o.anchored = false
	o.drift = 0
}

func (o *out) misses() (late, dropped int64) { return o.late.Load(), o.dropped.Load() }

func (o *out) queuedMs() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.pcm) / speaker.Channels * 1000 / speaker.Rate
}

// setVolume and setMuted go through the device's own player rather than the speaker underneath it: it
// owns the level, shows it in Home Assistant and remembers it across a restart.
func (o *out) setVolume(volume int) {
	media.Get().Set(max(0, min(volume, 100)) * speaker.VolumeSteps / 100)
}

func (o *out) setMuted(muted bool) { media.Get().Mute(muted) }

// Suspend and Resume do not keep a place: the rest of the house carried on, so this rejoins where they
// are now, which is what the frame index already says.
func (o *out) Suspend() {
	o.mu.Lock()
	o.held = true
	o.mu.Unlock()
	slog.Info("sendspin suspend", "queued_ms", o.queuedMs())
}

func (o *out) Resume() {
	o.mu.Lock()
	o.held = false
	o.mu.Unlock()
	slog.Info("sendspin resume", "queued_ms", o.queuedMs())
}

// Duck always quietens, never pauses, whatever config.Media.OnTurn says: a hole in one room of a
// whole-house stream is worse, and the canceller keeps a live reference.
func (o *out) Duck(on bool) {
	gain := float32(1)
	if on {
		gain = float32(math.Pow(10, float64(config.Get().Media.DuckDB)/20))
	}

	o.mu.Lock()
	o.gain = gain
	o.mu.Unlock()
}

// Requeue has nothing to do: the level is applied as frames are rendered, so none are waiting at the
// one they were decoded at.
func (o *out) Requeue() {}

func scale(s int16, gain float32) int16 {
	if gain == 1 {
		return s
	}
	return int16(max(math.MinInt16, min(float64(s)*float64(gain), math.MaxInt16)))
}
