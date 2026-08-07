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
	// feed the sampling jitter of "what is playing now" straight back into where audio lands.
	anchored bool
	frame    uint64
	at       int64

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
	ahead := o.clock.ServerToLocalTime(at).Sub(time.Now()) - speaker.HardwareTail
	o.frame = o.p.Written() + uint64(max(0, ahead.Seconds()*speaker.Rate))
	o.at = at
	o.anchored = true
	o.played = o.frame

	slog.Info("sendspin anchored", "frame", o.frame, "ahead_ms", ahead.Milliseconds())
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
	for i := range min(len(buf), len(o.pcm)) {
		buf[i] = scale(o.pcm[i], o.gain)
	}
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
}

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
