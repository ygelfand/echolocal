// Package mic owns the microphone array: one capture stream, held for the life of the process,
// with 16 kHz mono frames fanned out to whoever is listening.
package mic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ygelfand/echolocal/internal/android/prop"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/lib/alsa"
	"github.com/ygelfand/echolocal/internal/lib/audio"
	"github.com/ygelfand/echolocal/internal/lib/denoise"
	"github.com/ygelfand/echolocal/internal/service"
)

// The capture codec accepts one format only: 16 kHz, S24_3LE, 9 channels.
const (
	Rate     = 16000
	Channels = 9
	Bits     = 24

	Card          = 0
	CaptureDevice = 24

	period  = FrameSamples
	periods = 8
)

// Mics is how many of the nine channels are microphones. ch7 and ch8 are the playback loopback.
const Mics = 7

// Refs is the loopback pair that follows them, left then right.
const Refs = Channels - Mics

// CenterMic is the middle microphone: no arrival delay relative to the array, and usable with no
// beamformer at all.
const CenterMic = 6

// FrameSamples is the frame size handed to listeners, 20 ms at 16 kHz.
const FrameSamples = Rate / 50

// MediaService is the init service that holds the capture device on a fresh boot.
const MediaService = "media"

const (
	acquireRetry    = 250 * time.Millisecond
	acquireAttempts = 8
)

// Source is the capture stream. Listeners receive mono frames; a listener that cannot keep up
// misses frames rather than stalling the reader.
type Source struct {
	// The hardware, taken by Start and let go by Close. Nil in between: the handle outlives the
	// device so that a restart can take it again without every listener being rebuilt.
	devMu sync.Mutex
	pcm   *alsa.Capture

	mu        sync.Mutex
	listeners map[int]chan []int16
	raw       map[int]chan []byte
	next      int

	// dropped counts frames a listener was too slow to take. It matters more than it looks: the
	// wake model is streaming, so a missing frame corrupts its state rather than just losing audio.
	dropped atomic.Uint64

	history history

	// mixer is read by the reader and replaced from Home Assistant, both under mu.
	mixer  Mixer
	mixing config.Mixing

	// cancel belongs to the reader alone. cancelling is the switch, which anything may set, and is read
	// under mu with the mixer.
	cancel     *canceller
	cancelling bool

	// leveler and wasLeveling belong to the reader alone; leveling is the switch, which anything may
	// set.
	leveler     *leveler
	wasLeveling bool
	leveling    atomic.Bool

	// The same shape for the noise estimator, which also has state to throw away when it is turned off.
	denoiser     *denoise.Stream
	wasDenoising bool
	denoising    atomic.Bool

	// Which way the loudest sound is, as a beam, or -1 until something asks. finder belongs to the
	// reader; wantFacing is how anything else asks it to look.
	finder     *Beamformer
	facing     atomic.Int32
	wantFacing atomic.Bool
}

// SetMixing chooses how the microphones are combined. It takes effect on the next frame, and reports
// what it settled on, which differs from the request only when this build cannot do it.
func (s *Source) SetMixing(m config.Mixing) config.Mixing {
	mixer, settled := NewMixer(m)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.mixer, s.mixing = mixer, settled
	return settled
}

// Mixing is the combination in use.
func (s *Source) Mixing() config.Mixing {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mixing
}

// New makes the array without taking the hardware, so listeners can subscribe before there is
// anything to hear. Listen, Recent and the mixing setting work throughout; frames start at Start.
func New() *Source {
	mixer, mixing := NewMixer(config.Get().Microphone.Mixing)
	s := &Source{
		listeners:  map[int]chan []int16{},
		raw:        map[int]chan []byte{},
		mixer:      mixer,
		mixing:     mixing,
		leveler:    newLeveler(),
		finder:     NewBeamformer(),
		cancel:     newCanceller(),
		cancelling: config.Get().Microphone.Cancel,
		denoiser:   denoise.NewStream(Rate),
	}
	s.finder.hold = 1
	s.facing.Store(-1)
	s.leveling.Store(config.Get().Microphone.Leveling)
	s.denoising.Store(config.Get().Microphone.Denoise)
	return s
}

var (
	once   sync.Once
	shared *Source
)

func init() {
	// After the speaker: both are held for the life of the process, and the playback path is the one
	// the vendor's own services fight over.
	component.Register(component.Hardware, Get(), component.Order(7),
		component.Supervise(service.Restart(time.Second, 30*time.Second)))
}

// Get is the array. There is one, and everything that wants frames subscribes to it.
func Get() *Source {
	once.Do(func() { shared = New() })
	return shared
}

func (s *Source) Name() string { return "capture" }

// Start takes the capture device, off Android if it got there first, the same way the speaker does.
func (s *Source) Start(context.Context) error {
	err := s.open()
	if err == nil || !errors.Is(err, alsa.ErrBusy) {
		return err
	}

	slog.Warn("capture device busy, stopping "+MediaService+" to take it", "err", err)
	if err := prop.Stop(MediaService); err != nil {
		return err
	}
	defer func() {
		if err := prop.Start(MediaService); err != nil {
			slog.Error("restarting "+MediaService+" failed", "err", err)
		}
	}()

	for range acquireAttempts {
		time.Sleep(acquireRetry)

		err = s.open()
		if err == nil {
			slog.Info("capture device acquired")
			return nil
		}
		if !errors.Is(err, alsa.ErrBusy) {
			return err
		}
	}
	return err
}

// Acquire is New and Start together, for a tool that wants the array for the length of one command.
func Acquire() (*Source, error) {
	s := New()
	if err := s.Start(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Source) open() error {
	pcm, err := alsa.Open(Card, CaptureDevice, alsa.Config{
		Channels:   Channels,
		Rate:       Rate,
		Format:     alsa.FormatS24_3LE,
		Bits:       Bits,
		PeriodSize: period,
		Periods:    periods,
	})
	if err != nil {
		return fmt.Errorf("mic: opening capture: %w", err)
	}

	s.devMu.Lock()
	s.pcm = pcm
	s.devMu.Unlock()

	applyGain(config.Get().Microphone.Gain)
	return nil
}

// device is the hardware, or nil when it is not held.
func (s *Source) device() *alsa.Capture {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	return s.pcm
}

// Listen returns a channel of mono frames and a function that stops the subscription.
func (s *Source) Listen() (<-chan []int16, func()) {
	ch := make(chan []int16, 8)

	s.mu.Lock()
	id := s.next
	s.next++
	s.listeners[id] = ch
	s.mu.Unlock()

	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if c, ok := s.listeners[id]; ok {
			delete(s.listeners, id)
			close(c)
		}
	}
}

// ListenRaw returns a channel of interleaved frames, all nine channels as the hardware gives them.
// For characterising the array; the mono path is what detection and the pipeline use.
func (s *Source) ListenRaw() (<-chan []byte, func()) {
	ch := make(chan []byte, 8)

	s.mu.Lock()
	id := s.next
	s.next++
	s.raw[id] = ch
	s.mu.Unlock()

	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if c, ok := s.raw[id]; ok {
			delete(s.raw, id)
			close(c)
		}
	}
}

// Decode splits an interleaved frame into one slice per microphone, narrowed to 16 bits.
func Decode(raw []byte) [][]int16 { return decode(raw, 0, Mics) }

// Reference is the playback loopback, left and right. It is what was sent to the DAC rather than
// anything the microphones heard: bit exact, and decimated by the same hardware that decimates them,
// so it arrives already aligned with the microphones at their rate.
func Reference(raw []byte) [][]int16 { return decode(raw, Mics, Refs) }

func decode(raw []byte, first, n int) [][]int16 {
	const frameBytes = Channels * Bits / 8

	frames := len(raw) / frameBytes
	out := make([][]int16, n)
	for c := range out {
		out[c] = make([]int16, frames)
	}
	for f := range frames {
		off := f * frameBytes
		for c := range out {
			at := off + (first+c)*3
			out[c][f] = int16(audio.DecodeS24LE3(raw[at:at+3]) >> 8)
		}
	}
	return out
}

// Run reads until ctx is cancelled. It reads whether or not anyone is listening, because a stream
// left unread overruns and the hardware ring is only 160 ms deep.
func (s *Source) Run(ctx context.Context) error {
	pcm := s.device()
	if pcm == nil {
		return errors.New("mic: the capture device is not held")
	}
	raw := make([]byte, FrameSamples*Channels*Bits/8)

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		n, err := pcm.Read(raw)
		if err != nil {
			if errors.Is(err, alsa.ErrOverrun) {
				slog.Warn("capture overrun")
				continue
			}
			return err
		}
		s.broadcast(raw[:n])
	}
}

// broadcast hands the frame to every listener, dropping it for any that is behind. The mono mix is
// only computed when something wants it.
func (s *Source) broadcast(raw []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Everything downstream reads the same single channel, whichever way it was made: wake detection
	// and what Home Assistant transcribes should never disagree about what was heard.
	//
	// The recent history is kept whether or not anyone is listening: a wake word is only recognised
	// once it has been said, so by the time a turn starts, the words after it are already past.
	mics := Decode(raw)
	frame := s.mixer.Mix(mics)

	// While something is playing, the echo cancelled center microphone replaces the mix. It has to be
	// one fixed microphone: the filter learns a single acoustic path, and the beamformer would steer at
	// the loudest thing in the room, which during playback is the speaker being cancelled.
	if s.cancelling && s.cancel != nil {
		if cancelled := s.cancel.apply(raw, mics); cancelled != nil {
			frame = cancelled
		}
	}

	s.findFacing(mics)

	// After the echo canceller, so the estimator is not asked to learn the speaker as part of the
	// room, and before leveling, so the gain is not put back on a floor that has just been removed.
	if quiet := s.denoising.Load(); quiet {
		s.denoiser.Apply(frame)
		s.wasDenoising = true
	} else if s.wasDenoising {
		s.denoiser.Forget()
		s.wasDenoising = false
	}

	// Turning leveling off throws away what it learned, so a room it has adapted badly to is
	// recovered by switching it off and on rather than by restarting anything.
	on := s.leveling.Load()
	switch {
	case on:
		s.leveler.apply(frame)
	default:
		// Measured even with the gain switched off, because how loud the room is has watchers of its
		// own: the ring can be set to react to it, and that should not depend on a setting about what
		// Home Assistant hears.
		if s.wasLeveling {
			s.leveler.forget()
		}
		if len(frame) > 0 {
			s.leveler.observe(frame)
		}
	}
	s.wasLeveling = on
	s.remember(frame)

	for _, ch := range s.listeners {
		select {
		case ch <- frame:
		default:
			s.dropped.Add(1)
		}
	}

	if len(s.raw) == 0 {
		return
	}

	// The reader reuses its buffer, so raw listeners get their own copy.
	interleaved := make([]byte, len(raw))
	copy(interleaved, raw)
	for _, ch := range s.raw {
		select {
		case ch <- interleaved:
		default:
		}
	}
}

// Dropped is how many frames a listener has missed.
func (s *Source) Dropped() uint64 { return s.dropped.Load() }

// Close lets the device go. The Source stays usable and its listeners stay subscribed: Start can take
// the hardware again, which is how a restart works.
func (s *Source) Close() error {
	s.devMu.Lock()
	pcm := s.pcm
	s.pcm = nil
	s.devMu.Unlock()

	if pcm == nil {
		return nil
	}
	return pcm.Close()
}

// Mono takes the center microphone alone, narrowed from 24 bits to 16. The beamformed mix is what
// listeners get; this is for tools that need one microphone as it comes off the hardware.
func Mono(raw []byte) []int16 {
	const frameBytes = Channels * Bits / 8

	out := make([]int16, len(raw)/frameBytes)
	for i := range out {
		o := i*frameBytes + CenterMic*3
		out[i] = int16(audio.DecodeS24LE3(raw[o:o+3]) >> 8)
	}
	return out
}
