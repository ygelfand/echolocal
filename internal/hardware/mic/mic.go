package mic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ygelfand/libcountertop/pkg/assistant/voicematch"
	"github.com/ygelfand/libcountertop/pkg/audio/aec"
	"github.com/ygelfand/libcountertop/pkg/audio/alsa"
	"github.com/ygelfand/libcountertop/pkg/audio/capture"
	"github.com/ygelfand/libcountertop/pkg/audio/level"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/runtime/service"

	"github.com/ygelfand/echolocal/internal/android/prop"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/lib/audio"
	"github.com/ygelfand/echolocal/internal/lib/subband"
)

const (
	Rate = 16000
	Bits = 24

	Card = 0

	period  = FrameSamples
	periods = 8
)

const Refs = Channels - Mics

const FrameSamples = Rate / 50

// The init service holding the capture device on a fresh boot.
const MediaService = "media"

const (
	acquireRetry    = 250 * time.Millisecond
	acquireAttempts = 8
)

type Source struct {
	startup atomic.Pointer[sharedcomponent.Progress]
	devMu   sync.Mutex
	pcm     stream

	mu        sync.Mutex
	listeners map[int]*listener
	raw       map[int]chan []byte
	next      int

	dropped atomic.Uint64

	*capture.History
	*level.Leveler

	mixer  Mixer
	mixing config.Mixing

	cancel     *aec.Canceller
	cancelling bool
	cancelled  []int16

	adapt, frozen, reset atomic.Bool

	echoes map[int]chan Echo

	ref  []int16
	hold int

	lowCut *lowCut

	suppress *aec.Preprocessor

	finder     *Beamformer
	facing     atomic.Int32
	wantFacing atomic.Bool

	compass      *subband.Beamformer
	compassBeam  atomic.Int32
	compassAsked atomic.Int64
}

func (s *Source) SetMixing(m config.Mixing) config.Mixing {
	mixer, settled := NewMixer(m)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.mixer, s.mixing = mixer, settled
	return settled
}

func (s *Source) Mixing() config.Mixing {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mixing
}

func New() *Source {
	cfg := config.Get().Microphone
	mixer, mixing := NewMixer(cfg.Mixing)
	s := &Source{
		listeners:  map[int]*listener{},
		raw:        map[int]chan []byte{},
		echoes:     map[int]chan Echo{},
		History:    capture.NewHistory(Rate, voicematch.Span),
		Leveler:    newLeveler(cfg),
		mixer:      mixer,
		mixing:     mixing,
		finder:     NewBeamformer(),
		cancel:     newCanceller(aec.Taps),
		cancelling: cfg.Cancel,
		lowCut:     newLowCut(lowCutHz),
	}
	s.finder.hold = 1
	s.facing.Store(-1)
	s.compassBeam.Store(-1)
	s.adapt.Store(true)
	s.suppress = newSuppressor(s.cancel)
	return s
}

func newLeveler(cfg config.Microphone) *level.Leveler {
	return level.New(level.Config{
		Rate:        Rate,
		Frame:       FrameSamples,
		Sensitivity: cfg.Sensitivity,
		Leveling:    cfg.Leveling,
	})
}

var (
	once   sync.Once
	shared *Source
)

func init() {
	component.Register(sharedcomponent.Hardware, Get, sharedcomponent.Order(7),
		sharedcomponent.Supervise(service.Restart(time.Second, 30*time.Second)))
}

func Get() *Source {
	once.Do(func() { shared = New() })
	return shared
}

func (s *Source) Name() string { return "capture" }

func (s *Source) Start(context.Context) (err error) {
	defer func() {
		state := sharedcomponent.Progress{Done: err == nil}
		if err != nil {
			state.Failed = true
			state.Doing = err.Error()
		}
		s.startup.Store(&state)
	}()
	err = s.open()
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

func Acquire() (*Source, error) {
	s := New()
	if err := s.Start(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

type stream interface {
	Read(buf []byte) (int, error)
	Close() error
}

func (s *Source) open() error {
	pcm, err := openCapture()
	if err != nil {
		return fmt.Errorf("mic: opening capture: %w", err)
	}

	s.devMu.Lock()
	s.pcm = pcm
	s.devMu.Unlock()

	routeInputs()
	applyGain(config.Get().Microphone.Gain)
	return nil
}

func (s *Source) device() stream {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	return s.pcm
}

type listener struct {
	name      string
	ch        chan []int16
	dropped   uint64
	told      uint64
	unleveled bool
}

func (s *Source) Listen(name string) (<-chan []int16, func()) { return s.listen(name, false) }

func (s *Source) ListenUnleveled(
	name string,
) (<-chan []int16, func()) {
	return s.listen(name, true)
}

func (s *Source) listen(name string, unleveled bool) (<-chan []int16, func()) {
	l := &listener{name: name, ch: make(chan []int16, 8), unleveled: unleveled}

	s.mu.Lock()
	id := s.next
	s.next++
	s.listeners[id] = l
	s.mu.Unlock()

	return l.ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if was, ok := s.listeners[id]; ok {
			delete(s.listeners, id)
			close(was.ch)
			if lost := was.dropped - was.told; lost > 0 {
				slog.Warn("listener behind", "who", was.name, "frames", lost, "total", was.dropped)
			}
		}
	}
}

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

func Decode(raw []byte) [][]int16 { return decode(raw, 0, Mics) }

func DecodeAll(raw []byte) [][]int16 { return decode(raw, 0, Channels) }

// The loopback is the DAC feed, bit exact, decimated and aligned with the microphones by the same hardware.
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

// A stream left unread overruns: the hardware ring is 160 ms deep.
func (s *Source) Run(ctx context.Context) error {
	pcm := s.device()
	if pcm == nil {
		return errors.New("mic: the capture device is not held")
	}
	raw := make([]byte, FrameSamples*Channels*Bits/8)

	var overruns uint64
	report := time.NewTicker(dropReport)
	defer report.Stop()

	// An ALSA read can return short of a period, and the next read carries the rest.
	filled := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		select {
		case <-report.C:
			s.reportDrops()
		default:
		}

		n, err := pcm.Read(raw[filled:])
		if err != nil {
			if errors.Is(err, alsa.ErrOverrun) {
				if overruns++; overruns == 1 || overruns%100 == 0 {
					slog.Warn("capture overrun", "times", overruns)
				}
				filled = 0
				continue
			}
			return err
		}
		if filled += n; filled < len(raw) {
			continue
		}
		s.broadcast(raw)
		filled = 0
	}
}

const dropReport = 30 * time.Second

func (s *Source) reportDrops() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, l := range s.listeners {
		since := l.dropped - l.told
		if since == 0 {
			continue
		}
		l.told = l.dropped
		slog.Warn("listener behind", "who", l.name, "frames", since,
			"per_second", float64(since)/dropReport.Seconds(), "total", l.dropped)
	}
}

func (s *Source) broadcast(raw []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	mics := Decode(raw)
	frame := s.mixer.Mix(mics)
	heard := frame

	sounding := s.sounding(raw, len(mics[CenterMic]))
	s.SetPlayback(sounding)

	quiet := s.suppress != nil
	suppressed := false

	if s.cancelling && s.cancel != nil {
		if s.reset.Swap(false) {
			s.cancel.Reset()
		}
		s.cancel.SetAdapting(s.Adapting())

		switch {
		case sounding:
			var after func(int, []int16)
			if quiet {
				s.suppress.SetEcho(s.cancel.Filter(0))
				after = func(_ int, block []int16) { s.suppressBlock(block) }
			}
			if cancelled := s.cancelCenter(mics, after); cancelled != nil {
				frame, suppressed = cancelled, quiet
			}
		default:
			s.cancel.Idle()
		}
	}

	frame = append([]int16(nil), frame...)

	if quiet && !suppressed {
		s.suppress.SetEcho(nil)
		s.suppressBlock(frame)
	}
	s.lowCut.apply(frame)

	s.findFacing(mics)
	s.followBeam(mics)

	for _, l := range s.listeners {
		if l.unleveled {
			s.offer(l, append([]int16(nil), frame...))
		}
	}

	s.Process(heard, frame)
	s.Add(frame)

	for _, l := range s.listeners {
		if !l.unleveled {
			s.offer(l, frame)
		}
	}

	if len(s.echoes) > 0 {
		e := Echo{
			Mic:      mics[CenterMic],
			Ref:      append([]int16(nil), s.ref...),
			Out:      frame,
			Sounding: sounding,
		}
		for _, ch := range s.echoes {
			select {
			case ch <- e:
			default:
			}
		}
	}

	if len(s.raw) == 0 {
		return
	}

	interleaved := make([]byte, len(raw))
	copy(interleaved, raw)
	for _, ch := range s.raw {
		select {
		case ch <- interleaved:
		default:
		}
	}
}

func (s *Source) offer(l *listener, frame []int16) {
	select {
	case l.ch <- frame:
	default:
		l.dropped++
		s.dropped.Add(1)
	}
}

func (s *Source) Dropped() uint64 { return s.dropped.Load() }

func SetGain(db int) error {
	if err := config.Set().Microphone().Gain(db); err != nil {
		return err
	}
	applyGain(db)
	return nil
}

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

func Mono(raw []byte) []int16 {
	const frameBytes = Channels * Bits / 8

	out := make([]int16, len(raw)/frameBytes)
	for i := range out {
		o := i*frameBytes + CenterMic*3
		out[i] = int16(audio.DecodeS24LE3(raw[o:o+3]) >> 8)
	}
	return out
}
