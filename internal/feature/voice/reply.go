package voice

import (
	"context"
	"log/slog"
	"time"

	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

// replyResume is how much to hold after a dip, rather than the whole buffer again: rebuilding it every
// time turns a shortfall of a few milliseconds into a third of a second of silence.
const replyResume = 80 * time.Millisecond

// chunkQueue is how many chunks may wait for the errand. Home Assistant sends 32 ms at a time, so this
// is a couple of seconds of slack: enough that the conversation never waits to hand one over, and
// bounded because audio nobody has played yet is audio arriving faster than it can be heard.
const chunkQueue = 64

// stream is a reply arriving over the API, chunk by chunk, played as it goes. It belongs to the errand
// that plays it: the conversation writes chunks and reads the counters once the claim is done.
type stream struct {
	chunks chan []byte

	// buffer is how much to collect before playing any of it. Home Assistant paces itself to stay 384
	// ms ahead, which is enough when speech arrives faster than it plays and not when it does not.
	buffer time.Duration

	// held is audio waiting for the cushion to fill, and flowing says the cushion is built and audio
	// is going straight through.
	held    []int16
	flowing bool

	bytes       int
	peak        int
	splicesAt   uint64
	underrunsAt uint64

	// playingAt is when playback began, once the cushion had filled. Wall clock from here to the queue
	// running dry is what measures gapping; a starved buffer is silence the seam count never sees.
	playingAt time.Time
}

func newStream(splices, underruns uint64, buffer time.Duration) *stream {
	return &stream{
		chunks:      make(chan []byte, chunkQueue),
		buffer:      buffer,
		splicesAt:   splices,
		underrunsAt: underruns,
	}
}

// send hands a chunk to whoever is playing it. A queue this deep only fills if audio is arriving
// faster than it plays, in which case a dropped chunk is the least of it.
func (s *stream) send(data []byte) {
	select {
	case s.chunks <- data:
	default:
		slog.Warn("reply chunk dropped", "bytes", len(data))
	}
}

// done says no more chunks are coming, which is what ends the errand.
func (s *stream) done() { close(s.chunks) }

// play is the errand: it takes chunks until there are no more and queues them once enough has arrived
// to play through a dip. Returning hands over to the driver, which waits for the audio to be heard.
func (s *stream) play(ctx context.Context, p *speaker.Player, started func()) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case data, ok := <-s.chunks:
			if !ok {
				// Whatever is still held will never be joined by more, so it just plays.
				s.flush(p)
				return nil
			}
			s.take(data, p, started)
		}
	}
}

func (s *stream) take(data []byte, p *speaker.Player, started func()) {
	samples := make([]int16, len(data)/2)
	peak := 0
	for i := range samples {
		samples[i] = int16(uint16(data[i*2]) | uint16(data[i*2+1])<<8)
		peak = max(peak, int(samples[i]), -int(samples[i]))
	}

	// The first chunk is the pipeline delivering, so its limit is done with.
	if s.bytes == 0 {
		slog.Info("reply audio started", "bytes", len(data))
		started()
	}

	// How loud the reply arrives decides whether interpolating it can overflow, and the byte count
	// against how long it takes to say is how a wrong sample rate would show up.
	s.bytes += len(data)
	s.peak = max(s.peak, peak)

	s.held = append(s.held, samples...)
	if p.Queued() == 0 {
		s.flowing = false
	}

	want := s.buffer
	if !s.playingAt.IsZero() {
		want = replyResume
	}
	if !s.flowing && len(s.held) < heldSamples(want) {
		return
	}

	if s.playingAt.IsZero() {
		s.playingAt = time.Now()
	}
	s.flowing = true
	s.flush(p)
}

func (s *stream) flush(p *speaker.Player) {
	out := s.held
	s.held = nil
	if len(out) > 0 {
		p.PlayVoice(out)
	}
}

// heldSamples is a duration as a count of 16 kHz samples.
func heldSamples(d time.Duration) int {
	return int(d/time.Millisecond) * speaker.VoiceRate / 1000
}
