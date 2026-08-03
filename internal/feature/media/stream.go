package media

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/lib/noise"
	"github.com/ygelfand/echolocal/internal/lib/safe"
)

const (
	// ahead is how much audio may sit in the speaker's queue, in frames. It is what covers a pause in
	// the download, and it is also what has to be handed back when something else takes the speaker,
	// so it buys smoothness rather than being free.
	ahead = speaker.Rate

	// pace is how often the stream looks to see whether the queue has room.
	pace = 100 * time.Millisecond

	// chunk is how much is taken off the wire at once, about a sixth of a second.
	chunk = 32 * 1024

	// stall is how long one read may produce nothing before the track is given up on.
	stall = 30 * time.Second
)

// frame is one sample on every channel.
const frame = speaker.Channels * 2

// Stream plays a url through the speaker. Home Assistant converts the source with ffmpeg first, so
// what arrives is a WAV header followed by samples already at the playback rate: no decoder here,
// and nothing to resample.
//
// It streams rather than downloading. A track runs for minutes and the device has no room for one,
// so it keeps a second or so ahead of the speaker and reads no faster than it plays.
//
// It is the speaker driver's background: a reply or an announcement takes the speaker and this
// yields, carrying on from where it was rather than starting again.
type Stream struct {
	out     *speaker.Player
	changed func()

	mu    sync.Mutex
	track *track

	// holds counts what has taken the speaker: a turn, a reply, an announcement. Playing resumes
	// when the last of them gives it back, which is why it counts rather than being a flag.
	holds  int
	paused bool

	// duckHeld is whether the duck was the thing that suspended, which only happens when the setting
	// says pause. Ending the duck must not release a hold that a reply put there.
	duckHeld bool

	// gate is closed to let the stream carry on, and non-nil for as long as it may not.
	gate chan struct{}

	// rewind is what was queued but not heard when the speaker was taken away, put back at the
	// front when playing resumes so the track carries on rather than jumping forward.
	rewind []int16

	// write is held while samples go into the queue, so taking the queue away cannot be followed by
	// the stream refilling it behind the sound that displaced it.
	write sync.Mutex

	// gain is what queue is multiplying by and target is what it is heading for, both under write
	// alongside the samples they scale. Moving rather than jumping: a step change of 15 dB is a click,
	// and a ramp over a few tens of milliseconds is what makes it sound deliberate.
	gain, target float32
}

// track is one thing being played. Identity is the point: a track that has been replaced knows not
// to report itself finished.
type track struct {
	// item names it in the log, and sounds is what is being generated when it is not a url.
	item   string
	sounds []string
	cancel context.CancelFunc
}

// NewStream builds the stream and registers it with the speaker driver. changed is called whenever
// what it is doing changes, which is what tells Home Assistant.
func NewStream(sound *speaker.Driver, out *speaker.Player, changed func()) *Stream {
	m := &Stream{out: out, changed: changed, gain: 1, target: 1}
	sound.Yields(m)
	return m
}

// rampSamples is how many interleaved samples a full move between silence and full level takes, so
// 60 ms at the rate the codec runs. Long enough not to click, short enough that the reply is not
// already talking over the track at full volume.
const rampSamples = speaker.Rate * speaker.Channels * 60 / 1000

// Duck implements speaker.Background: a turn has started or ended.
//
// What it does about it is this end's decision, because only this end knows the difference between a
// song and a doorbell. A track lowers itself and keeps playing; anyone who would rather have silence
// under a reply sets it to pause, and then this is the same suspend a claim would have done.
func (m *Stream) Duck(on bool) {
	if m == nil {
		return
	}

	if on {
		if config.Get().Media.OnTurn == config.OnTurnPause {
			m.mu.Lock()
			m.duckHeld = true
			m.mu.Unlock()

			m.Suspend()
			return
		}

		level := float32(math.Pow(10, float64(config.Get().Media.DuckDB)/20))
		m.write.Lock()
		m.target = level
		m.write.Unlock()

		m.reduce()
		return
	}

	// Both are undone whatever the setting was when the turn began, since it can be changed in the
	// middle of one. Resuming is conditional on this having been what suspended: holds is shared with
	// the claims, and releasing one of theirs would put music back underneath a reply.
	m.write.Lock()
	m.target = 1
	m.write.Unlock()

	m.mu.Lock()
	held := m.duckHeld
	m.duckHeld = false
	m.mu.Unlock()

	if held {
		m.Resume()
	}
}

// Play starts a url, replacing whatever was playing.
//
// It does not take the speaker from a reply or an announcement that is sounding. Those are seconds
// long and end on their own, and the track waits behind them rather than talking over them.
func (m *Stream) Play(url string) {
	if m == nil {
		slog.Warn("asked to play media with no speaker", "url", url)
		return
	}

	t, ctx := m.start(&track{item: url})
	slog.Info("playing media", "url", url)

	safe.Go("media", func() {
		err := m.run(ctx, t, url)
		if err != nil && ctx.Err() == nil {
			slog.Error("playing media failed", "err", err)
		}
		m.finished(t)
	})
}

// PlayNoise runs generated sound instead of a url, and does not stop until it is stopped: that is the
// point of it. Everything else about it is a track, so a turn ducks it, the buttons set its level and
// the media player reports it.
func (m *Stream) PlayNoise(sounds ...string) {
	if m == nil {
		slog.Warn("asked to play noise with no speaker", "sounds", sounds)
		return
	}

	fill := noise.Mix(speaker.Rate, sounds...)
	if fill == nil {
		slog.Warn("no such sound", "sounds", sounds)
		return
	}

	t, ctx := m.start(&track{item: strings.Join(sounds, " and "), sounds: sounds})
	slog.Info("playing noise", "sounds", sounds)

	safe.Go("noise", func() {
		err := m.generate(ctx, t, fill)
		if err != nil && ctx.Err() == nil {
			slog.Error("playing noise failed", "err", err)
		}
		m.finished(t)
	})
}

// Noise is what is being generated, empty when what is playing came from somewhere else. It is what
// keeps the entities honest when a track or a stop displaces the noise.
func (m *Stream) Noise() []string {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.track == nil {
		return nil
	}
	return m.track.sounds
}

// start makes a track the one being played, dropping whatever was.
func (m *Stream) start(t *track) (*track, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel

	m.mu.Lock()
	previous := m.track
	m.track, m.paused, m.rewind = t, false, nil
	if m.holds == 0 {
		m.unblock()
	} else {
		m.block()
	}
	m.mu.Unlock()

	previous.stop()
	m.flush()
	m.changed()
	return t, ctx
}

// Pause stops the track where it is. What was queued but not heard is kept, so resuming does not
// skip it.
func (m *Stream) Pause() {
	if m == nil {
		return
	}

	m.mu.Lock()
	if m.track == nil || m.paused {
		m.mu.Unlock()
		return
	}
	m.paused = true
	m.block()
	ours := m.holds == 0
	m.mu.Unlock()

	if ours {
		m.keep()
	}
	m.changed()
}

// Unpause carries on from where Pause stopped.
func (m *Stream) Unpause() {
	if m == nil {
		return
	}

	m.mu.Lock()
	if m.track == nil || !m.paused {
		m.mu.Unlock()
		return
	}
	m.paused = false
	if m.holds == 0 {
		m.unblock()
	}
	m.mu.Unlock()

	m.changed()
}

// Stop ends the track. There is nothing to come back to afterwards.
func (m *Stream) Stop() {
	if m == nil {
		return
	}

	m.mu.Lock()
	t := m.track
	m.track, m.paused, m.rewind = nil, false, nil
	m.unblock()
	m.mu.Unlock()

	if t == nil {
		return
	}
	t.stop()
	m.flush()
	m.changed()
}

// Suspend implements speaker.Background: something else wants the speaker.
func (m *Stream) Suspend() {
	if m == nil {
		return
	}

	m.mu.Lock()
	m.holds++
	if m.track == nil || m.holds > 1 {
		m.mu.Unlock()
		return
	}
	m.block()
	m.mu.Unlock()

	m.keep()
}

// Resume implements speaker.Background: the speaker is free again.
func (m *Stream) Resume() {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.holds > 0 {
		m.holds--
	}
	if m.holds == 0 && !m.paused {
		m.unblock()
	}
}

// Playing reports whether a track is loaded and not paused, which is what Home Assistant is told.
// A track that is only waiting for a reply to finish is still playing: it is going to carry on
// without anyone asking it to.
func (m *Stream) Playing() (playing, paused bool) {
	if m == nil {
		return false, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.track != nil && !m.paused, m.track != nil && m.paused
}

// block and unblock hold and release the stream. Both want mu.
func (m *Stream) block() {
	if m.gate == nil {
		m.gate = make(chan struct{})
	}
}

func (m *Stream) unblock() {
	if m.gate != nil {
		close(m.gate)
		m.gate = nil
	}
}

// flush throws away audio that is ours to throw away. While something else holds the speaker the
// queue belongs to it, and emptying it would cut off a reply.
func (m *Stream) flush() {
	m.mu.Lock()
	held := m.holds > 0
	m.mu.Unlock()
	if held {
		return
	}

	m.write.Lock()
	defer m.write.Unlock()
	m.out.Drain()
}

// keep empties the queue and remembers what was in it. Taken rather than dropped: this is the
// middle of a song, and what has not been heard is where playing has to start again from.
func (m *Stream) keep() {
	m.write.Lock()
	defer m.write.Unlock()

	kept := m.out.Take()
	if len(kept) == 0 {
		// Anything already stashed is still what has not been heard.
		return
	}

	m.mu.Lock()
	m.rewind = kept
	m.mu.Unlock()
}

// replay puts back what Suspend took, once the speaker is ours again.
func (m *Stream) replay() {
	m.write.Lock()
	defer m.write.Unlock()

	m.mu.Lock()
	back := m.rewind
	if m.gate != nil || len(back) == 0 {
		m.mu.Unlock()
		return
	}
	m.rewind = nil
	m.mu.Unlock()

	m.out.Play(back)
}

// finished clears the track once it has played out, unless it has already been replaced.
func (m *Stream) finished(t *track) {
	m.mu.Lock()
	if m.track != t {
		m.mu.Unlock()
		return
	}
	m.track, m.paused, m.rewind = nil, false, nil
	m.unblock()
	m.mu.Unlock()

	slog.Info("media finished", "item", t.item)
	m.changed()
}

func (t *track) stop() {
	if t != nil {
		t.cancel()
	}
}

// run fetches the url and feeds it to the speaker as it arrives.
func (m *Stream) run(ctx context.Context, t *track, url string) error {
	// No timeout on the client: a track takes as long as it takes. What is bounded is a single read,
	// because a wedged connection otherwise holds the track open for as long as the kernel keeps
	// retrying — minutes of a player reporting that it is playing while nothing comes out.
	fetch, giveUp := context.WithCancel(ctx)
	defer giveUp()

	req, err := http.NewRequestWithContext(fetch, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}

	body := bufio.NewReaderSize(resp.Body, chunk)
	if err := header(body); err != nil {
		return err
	}

	buf := make([]byte, chunk)
	for {
		if err := m.wait(ctx); err != nil {
			return err
		}

		// Armed only around the read: a track waiting for a turn to finish is not stalled, and
		// counting that time would end it for being interrupted.
		watchdog := time.AfterFunc(stall, giveUp)
		n, err := io.ReadFull(body, buf)
		watchdog.Stop()

		if n >= frame {
			m.feed(t, buf[:n-n%frame])
		}
		switch {
		case err == nil:
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			return m.settle(ctx)
		case fetch.Err() != nil && ctx.Err() == nil:
			return fmt.Errorf("nothing arrived for %s", stall)
		default:
			return err
		}
	}
}

// wait holds the stream until it may play and the queue has room for more.
func (m *Stream) wait(ctx context.Context) error {
	for {
		m.mu.Lock()
		gate := m.gate
		m.mu.Unlock()

		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		m.replay()
		if m.out.Queued() < ahead {
			return nil
		}

		select {
		case <-time.After(pace):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// settle waits for what has been queued to play out, so the track is not reported finished while
// the last of it is still sounding.
func (m *Stream) settle(ctx context.Context) error {
	for {
		if err := m.wait(ctx); err != nil {
			return err
		}
		if m.out.Queued() == 0 {
			return nil
		}

		select {
		case <-time.After(pace):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// generate feeds the speaker from a generator instead of a socket, for as long as the track lasts.
// Both channels get the same samples: one enclosure, one driver.
//
// The buffer is filled again each time round, so queue is free to scale it in place on its way out.
func (m *Stream) generate(ctx context.Context, t *track, fill noise.Fill) error {
	mono := make([]float32, chunk/frame)
	samples := make([]int16, len(mono)*speaker.Channels)

	for {
		if err := m.wait(ctx); err != nil {
			return err
		}

		fill(mono)
		for i, v := range mono {
			s := int16(v * math.MaxInt16)
			samples[i*speaker.Channels] = s
			samples[i*speaker.Channels+1] = s
		}
		m.queue(t, samples)
	}
}

func (m *Stream) feed(t *track, pcm []byte) {
	samples := make([]int16, len(pcm)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(pcm[i*2:]))
	}
	m.queue(t, samples)
}

// queue hands samples over, unless the speaker was taken away in the meantime or this is no longer
// the track being played.
//
// The track has to be checked as well as the gate. Stopping opens the gate to release the goroutine
// waiting on it, and what that goroutine does next is read whatever the socket already holds — so
// without this it can put a chunk of a track that has been stopped into a queue that was just
// flushed, and the speaker plays it.
func (m *Stream) queue(t *track, samples []int16) {
	m.write.Lock()
	defer m.write.Unlock()

	m.mu.Lock()
	stale := m.gate != nil || m.track != t
	m.mu.Unlock()
	if stale {
		return
	}

	m.attenuate(samples)
	m.out.Play(samples)
}

// reduce applies the duck to audio that is already queued.
//
// ahead is a whole second of music sitting in the speaker's queue, and the room hears that before
// anything scaled on its way in — so the ramp has to be applied to what is already there to be heard
// when it matters.
//
// Only the track is in the queue at this point: a chime is mixed in after ducking, and so keeps its own
// level rather than fading with what is underneath it.
func (m *Stream) reduce() {
	m.write.Lock()
	defer m.write.Unlock()

	queued := m.out.Take()
	if len(queued) == 0 {
		return
	}
	m.attenuate(queued)
	m.out.Play(queued)
}

// attenuate applies the duck, moving towards the target rather than jumping to it. Wants write, which
// queue already holds.
//
// It rewrites the caller's slice, which is only ever a buffer feed just decoded. What replay puts back
// has been through here already and must not be scaled twice.
func (m *Stream) attenuate(samples []int16) {
	if m.gain == 1 && m.target == 1 {
		return
	}

	const step = 1.0 / rampSamples
	for i, s := range samples {
		switch {
		case m.gain < m.target:
			m.gain = min(m.gain+step, m.target)
		case m.gain > m.target:
			m.gain = max(m.gain-step, m.target)
		}
		samples[i] = int16(float32(s) * m.gain)
	}
}

// header reads past the WAV header and leaves the reader on the first sample.
//
// Home Assistant streams the file as ffmpeg produces it, which means the sizes in the header were
// written before the length was known: the data chunk runs until the connection ends, whatever it
// claims. The format is worth checking, though — the wrong rate or channel count is a track played
// at the wrong speed rather than an error anyone would see.
func header(r *bufio.Reader) error {
	var riff [12]byte
	if _, err := io.ReadFull(r, riff[:]); err != nil {
		return fmt.Errorf("reading the WAVE header: %w", err)
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return fmt.Errorf("not a WAVE stream: %q", riff[0:4])
	}

	for {
		var head [8]byte
		if _, err := io.ReadFull(r, head[:]); err != nil {
			return fmt.Errorf("reading a WAVE chunk: %w", err)
		}
		id := string(head[0:4])
		size := int64(binary.LittleEndian.Uint32(head[4:8]))

		if id == "data" {
			return nil
		}

		// Everything before the samples is a handful of bytes. A size larger than that is a stream
		// that is not what it says it is, and allocating from it is how that becomes our problem.
		if size > chunk {
			return fmt.Errorf("%q chunk is %d bytes", id, size)
		}

		body := make([]byte, size+size%2)
		if _, err := io.ReadFull(r, body); err != nil {
			return fmt.Errorf("reading the %q chunk: %w", id, err)
		}
		if id == "fmt " {
			if err := supported(body[:size]); err != nil {
				return err
			}
		}
	}
}

// supported checks a fmt chunk against what the codec takes.
func supported(fmtChunk []byte) error {
	if len(fmtChunk) < 16 {
		return fmt.Errorf("short fmt chunk: %d bytes", len(fmtChunk))
	}

	channels := binary.LittleEndian.Uint16(fmtChunk[2:])
	rate := binary.LittleEndian.Uint32(fmtChunk[4:])
	bits := binary.LittleEndian.Uint16(fmtChunk[14:])

	if channels != speaker.Channels || rate != speaker.Rate || bits != 16 {
		return fmt.Errorf("cannot play %d Hz %d channel %d bit audio", rate, channels, bits)
	}
	return nil
}
