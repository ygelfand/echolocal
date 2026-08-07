package speaker

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/lib/alsa"
	"github.com/ygelfand/echolocal/internal/lib/hook"
	"github.com/ygelfand/echolocal/internal/lib/safe"
	"github.com/ygelfand/echolocal/internal/service"
)

// The playback codec accepts one format only: 48 kHz, S16_LE, stereo.
const (
	Rate     = 48000
	Channels = 2
	Bits     = 16

	period  = 1024
	periods = 4

	// codecSettle is how long the codec sits powered and idle before the amplifier is enabled,
	// measured from the vendor HAL doing the same thing on a route change.
	codecSettle = 1100 * time.Millisecond
)

// VoiceRate is the rate Home Assistant's pipeline works at, and VoiceUpsample how many playback
// samples each of its samples becomes.
const (
	VoiceRate     = 16000
	VoiceUpsample = Rate / VoiceRate
)

const (
	Card           = 0
	PlaybackDevice = 23

	// AmpSwitch gates the speaker.
	AmpSwitch = "Ext_Speaker_Amp_Switch"
)

// Player owns the speaker: one playback stream held open for the life of the process, with the
// amplifier enabled while it runs.
type Player struct {
	// The hardware, taken by Start and let go by Close. Nil in between: the handle outlives the
	// device so that a restart can take it again without everything holding a Player being rebuilt.
	devMu sync.Mutex
	pb    *alsa.Playback
	mixer *alsa.Mixer

	// Output changed: a headphone was plugged in or pulled out.
	OnOutput hook.Hook[Output]

	volume atomic.Uint32 // linear gain, derived from step and the current output's curve
	step   atomic.Int32

	pathMu sync.Mutex
	out    Output

	voiceMu    sync.Mutex
	voice      Resampler
	resampling config.Resampling
	splices    atomic.Uint64

	// underruns is the card running out while we were away. Write blocks against the whole ring, so
	// each one means the loop was starved for as long as the ring is deep.
	underruns atomic.Uint64

	// deaf counts what was thrown away for want of a device.
	deaf atomic.Uint64

	mu      sync.Mutex
	pending []int16 // interleaved stereo waiting to go out

	// written counts frames handed to the card, which is what a Source places audio against.
	written atomic.Uint64

	srcMu  sync.Mutex
	src    Source
	srcBuf []int16
}

// Source is asked for the frames the card is about to play, addressed by absolute output frame index.
// A room playing along with the house needs that: where audio lands has to follow from when the server
// said to play it, not from when it happened to arrive.
type Source interface {
	// Render fills out with the frames starting at output frame index at, silence where it has none.
	Render(at uint64, out []int16)
}

// Attach sets the Source, or clears it with nil. Only one at a time: two things placing audio by
// absolute frame would be two things deciding what the room plays.
func (p *Player) Attach(s Source) {
	p.srcMu.Lock()
	defer p.srcMu.Unlock()
	p.src = s
}

// Written is the output frame index of the next frame to be handed to the card.
func (p *Player) Written() uint64 { return p.written.Load() }

// New makes the speaker without taking the hardware, so callers can hold it before there is anything
// to play through. Audio queued before Start waits; Volume and the rest work throughout.
func New() *Player {
	p := &Player{out: DetectOutput()}
	p.voice, p.resampling = NewResampler(config.ResampleSinc)
	p.SetVolume(VolumeSteps)
	return p
}

var (
	once   sync.Once
	shared *Player
)

func init() {
	// The audio devices are held for the life of the process: whatever is free, Android takes. Losing
	// one to Android is what a restart takes back, which is why the handle outlives the service.
	//
	// The speaker also feeds silence while idle, because the amplifier hisses when nothing drives the
	// DAC and toggling it pops.
	component.Register(component.Hardware, Get(), component.Order(6),
		component.Supervise(service.Restart(time.Second, 30*time.Second)))
}

// Get is the speaker. There is one, and everything audible goes through it.
func Get() *Player {
	once.Do(func() { shared = New() })
	return shared
}

func (p *Player) Name() string { return "speaker" }

// open takes the playback stream and the mixer. Mixer writes happen in Run: the first one enumerates
// the card, which takes over a second.
func (p *Player) open() error {
	m, err := alsa.OpenMixer(Card)
	if err != nil {
		return fmt.Errorf("speaker: opening mixer: %w", err)
	}

	pb, err := alsa.OpenPlayback(Card, PlaybackDevice, alsa.Config{
		Channels:   Channels,
		Rate:       Rate,
		Format:     alsa.FormatS16_LE,
		Bits:       Bits,
		PeriodSize: period,
		Periods:    periods,
	})
	if err != nil {
		_ = m.Close()
		return fmt.Errorf("speaker: opening playback: %w", err)
	}

	p.devMu.Lock()
	p.pb, p.mixer = pb, m
	p.devMu.Unlock()
	return nil
}

// device is the hardware, or nils when it is not held.
func (p *Player) device() (*alsa.Playback, *alsa.Mixer) {
	p.devMu.Lock()
	defer p.devMu.Unlock()
	return p.pb, p.mixer
}

// route applies the output's sequence. The amplifier stays off: it is enabled separately, after
// the codec has settled.
func (p *Player) route() {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	p.apply(pathSequence[p.out])
}

// amp switches the speaker amplifier.
func (p *Player) amp(on bool) {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()

	if p.out != OutputSpeaker {
		return
	}
	value := "Off"
	if on {
		value = "On"
	}
	p.apply([]kctl{{name: AmpSwitch, value: value}})
}

// Output reports which output the player is driving.
func (p *Player) Output() Output {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	return p.out
}

// setOutput moves the codec between the speaker and the headphone jack. The gain is re-derived
// because each output has its own curve.
func (p *Player) setOutput(out Output) {
	p.pathMu.Lock()
	if p.out == out {
		p.pathMu.Unlock()
		return
	}
	p.out = out
	p.apply([]kctl{{name: AmpSwitch, value: "Off"}})
	if out == OutputSpeaker {
		p.apply(headphoneOff)
	}
	p.apply(pathSequence[out])
	p.pathMu.Unlock()

	time.Sleep(codecSettle)
	p.amp(true)

	p.SetVolume(int(p.step.Load()))
	slog.Info("output changed", "output", out)
	p.OnOutput.Emit(out)
}

// watchJack follows the headphone jack.
func (p *Player) watchJack(ctx context.Context) {
	t := time.NewTicker(jackPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.setOutput(DetectOutput())
		}
	}
}

// apply writes a mixer sequence in order, logging failures and continuing.
func (p *Player) apply(seq []kctl) {
	_, mixer := p.device()
	if mixer == nil {
		return
	}

	for _, c := range seq {
		var err error
		switch {
		case c.value != "":
			err = mixer.SetEnum(c.name, c.value)
		case c.blob != nil:
			err = mixer.SetBytes(c.name, c.blob)
		default:
			err = mixer.SetInt(c.name, uint32(c.level))
		}
		if err != nil {
			slog.Error("mixer write failed", "control", c.name, "err", err)
		}
	}
}

// Run feeds the stream until ctx is cancelled, writing silence when nothing is queued.
func (p *Player) Run(ctx context.Context) error {
	pb, _ := p.device()
	if pb == nil {
		return errors.New("speaker: the playback device is not held")
	}
	buf := make([]byte, period*Channels*Bits/8)

	// The order the vendor HAL uses on a route change: route with the amplifier off, let the
	// codec sit idle, enable the amplifier, then feed.
	out := p.Output()
	p.apply(initSequence)
	p.route()

	select {
	case <-ctx.Done():
		return nil
	case <-time.After(codecSettle):
	}

	p.amp(true)
	slog.Info("playback path up", "output", out)
	p.OnOutput.Emit(out)
	safe.Go("jack watcher", func() { p.watchJack(ctx) })

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		p.fill(buf)
		if err := p.send(ctx, pb, buf); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		p.written.Add(period)
	}
}

// send writes one period, putting the same buffer back after an underrun rather than refilling. fill
// has already taken these frames off the queue, so starting over would play the period after them and
// lose these.
func (p *Player) send(ctx context.Context, to io.Writer, buf []byte) error {
	for {
		_, err := to.Write(buf)
		if err == nil {
			return nil
		}
		if err != alsa.ErrUnderrun {
			return err
		}
		if n := p.underruns.Add(1); n == 1 || n%100 == 0 {
			slog.Warn("playback underrun", "times", n)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// fill takes what is queued and pads the rest with silence.
func (p *Player) fill(buf []byte) {
	p.mu.Lock()
	take := min(len(p.pending), period*Channels)
	chunk := p.pending[:take]
	p.pending = p.pending[take:]
	if len(p.pending) == 0 {
		p.pending = nil
	}
	p.mu.Unlock()

	// The queue emptied part way through this buffer, so silence is spliced into whatever was
	// playing. At the end of a reply that is expected; repeatedly during one means audio is
	// arriving slower than it plays out.
	if take > 0 && take < period*Channels {
		p.splices.Add(1)
	}

	// The queue and a Source place audio differently — one in order, one by frame index — so they are
	// summed rather than one replacing the other, and a reply still sounds over a room stream.
	rendered := p.render()

	gain := p.Volume()
	for i := range period * Channels {
		var s int32
		if i < len(chunk) {
			s = int32(chunk[i])
		}
		if i < len(rendered) {
			s += int32(rendered[i])
		}
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(float32(clamp(s))*gain)))
	}
}

// render asks the Source for this period. Called only from the write loop, so the buffer is reused.
func (p *Player) render() []int16 {
	p.srcMu.Lock()
	src := p.src
	p.srcMu.Unlock()

	if src == nil {
		return nil
	}
	if cap(p.srcBuf) < period*Channels {
		p.srcBuf = make([]int16, period*Channels)
	}
	p.srcBuf = p.srcBuf[:period*Channels]
	clear(p.srcBuf)

	src.Render(p.written.Load(), p.srcBuf)
	return p.srcBuf
}

// Play queues interleaved stereo samples.
//
// Audio offered while the device is not held is dropped rather than queued. Nothing is draining the
// queue then, so it would grow for as long as the speaker stayed away — and a device that cannot play
// should say so in the log rather than in memory.
func (p *Player) Play(samples []int16) {
	if pb, _ := p.device(); pb == nil {
		if n := p.deaf.Add(1); n == 1 || n%100 == 0 {
			slog.Warn("audio dropped, no playback device", "times", n)
		}
		return
	}

	p.mu.Lock()
	p.pending = append(p.pending, samples...)
	p.mu.Unlock()
}

// Take empties the queue and hands back what had not been played, so a sound that yields to another
// can carry on from where it was rather than skipping whatever it had queued.
func (p *Player) Take() []int16 {
	p.mu.Lock()
	defer p.mu.Unlock()

	pending := p.pending
	p.pending = nil
	return pending
}

// Adjust rewrites the queue in place. Taking it out and putting it back would leave it empty in
// between, and fill pads an empty queue with silence.
func (p *Player) Adjust(rewrite func([]int16)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rewrite(p.pending)
}

// PlayVoice queues 16 kHz mono audio, which is what Home Assistant's pipeline sends. The codec
// only accepts 48 kHz stereo, so it is interpolated up and duplicated across both channels. The
// resampler carries state between calls, so a reply delivered in chunks is one continuous signal.
func (p *Player) PlayVoice(mono []int16) {
	p.voiceMu.Lock()
	out := p.voice.Run(mono, make([]int16, 0, len(mono)*VoiceUpsample*Channels))
	p.voiceMu.Unlock()

	p.Play(out)
}

// Drain discards anything queued but not yet played, for a barge-in. The resampler's history goes
// with it: whatever comes next is a different utterance.
func (p *Player) Drain() {
	p.mu.Lock()
	p.pending = nil
	p.mu.Unlock()

	p.voiceMu.Lock()
	p.voice.Reset()
	p.voiceMu.Unlock()
}

// SetResampling picks how voice is stretched to the playback rate and reports what it settled on,
// which is the filter for anything this build does not have. It takes effect on the next reply.
func (p *Player) SetResampling(r config.Resampling) config.Resampling {
	p.voiceMu.Lock()
	defer p.voiceMu.Unlock()

	p.voice, p.resampling = NewResampler(r)
	return p.resampling
}

// Resampling is the one in use.
func (p *Player) Resampling() config.Resampling {
	p.voiceMu.Lock()
	defer p.voiceMu.Unlock()
	return p.resampling
}

// Splices counts how many times the queue has run dry part way through a buffer.
func (p *Player) Splices() uint64 { return p.splices.Load() }

func (p *Player) Underruns() uint64 { return p.underruns.Load() }

// Clipped counts voice samples that came out of the resampler past full scale.
func (p *Player) Clipped() uint64 {
	p.voiceMu.Lock()
	defer p.voiceMu.Unlock()
	return p.voice.Clipped()
}

// Queued reports how many frames are waiting, so a caller can tell when playback has finished.
func (p *Player) Queued() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending) / Channels
}

// Note is one tone in a chime. A zero frequency is a rest.
type Note struct {
	Freq float64
	Ms   int
}

// Beep queues a tone.
func (p *Player) Beep(freq float64, ms int, level float64) {
	p.Chime(level, Note{Freq: freq, Ms: ms})
}

// Chime sounds notes back to back, each shaped by the same short envelope so the joins do not click.
//
// A tone is feedback, so it mixes into whatever is queued rather than waiting behind it: queued audio
// is a reply's cushion or, when the reply came as a file, all of it, and a beep that waits is a beep
// that arrives after the answer.
func (p *Player) Chime(level float64, notes ...Note) {
	var out []int16
	for _, n := range notes {
		out = append(out, tone(n, level)...)
	}
	p.Overlay(out)
}

// Overlay mixes samples into what is already queued, extending the queue if they outlast it. Sums are
// clamped: two things at once are louder than either, and wrapping would turn that into a crack.
func (p *Player) Overlay(samples []int16) {
	if pb, _ := p.device(); pb == nil {
		p.Play(samples)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = mix(p.pending, samples)
}

// mix sums add into the front of into, extending it if add outlasts it.
func mix(into, add []int16) []int16 {
	for i, s := range add {
		if i >= len(into) {
			return append(into, add[i:]...)
		}
		into[i] = clamp(int32(into[i]) + int32(s))
	}
	return into
}

func clamp(v int32) int16 {
	switch {
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	}
	return int16(v)
}

func tone(n Note, level float64) []int16 {
	frames := Rate * n.Ms / 1000
	out := make([]int16, frames*Channels)

	for i := range frames {
		if n.Freq == 0 {
			continue
		}
		env := math.Min(1, math.Min(float64(i), float64(frames-i))/float64(Rate/200))
		s := int16(level * env * math.MaxInt16 * math.Sin(2*math.Pi*n.Freq*float64(i)/Rate))
		out[i*Channels] = s
		out[i*Channels+1] = s
	}
	return out
}

// SetVolume sets the playback level as a step on the vendor's curve for the current output.
func (p *Player) SetVolume(step int) {
	step = max(0, min(step, VolumeSteps))
	p.step.Store(int32(step))
	p.volume.Store(math.Float32bits(gainForStep(p.Output(), step)))
}

// Step is the current volume step.
func (p *Player) Step() int { return int(p.step.Load()) }

// Volume is the current linear gain.
func (p *Player) Volume() float32 { return math.Float32frombits(p.volume.Load()) }

// Close mutes the codec, turns the amplifier off and closes the stream.
// Close mutes the codec, turns the amplifier off and lets the device go. The Player stays usable:
// Start can take it again, which is how a restart works.
func (p *Player) Close() error {
	p.apply(initSequence)

	p.devMu.Lock()
	pb, mixer := p.pb, p.mixer
	p.pb, p.mixer = nil, nil
	p.devMu.Unlock()

	if mixer != nil {
		_ = mixer.Close()
	}
	if pb == nil {
		return nil
	}
	return pb.Close()
}
