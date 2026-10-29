package speaker

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ygelfand/libcountertop/pkg/audio/alsa"
	"github.com/ygelfand/libcountertop/pkg/audio/mix"
	"github.com/ygelfand/libcountertop/pkg/audio/tone"
	"github.com/ygelfand/libcountertop/pkg/audio/upsample"
	"github.com/ygelfand/libcountertop/pkg/audio/volume"
	"github.com/ygelfand/libcountertop/pkg/hook"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
	"github.com/ygelfand/libcountertop/pkg/runtime/service"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/lib/asp"
)

// The playback codec accepts one format only: 48 kHz, S16_LE, stereo.
const (
	Rate     = 48000
	Channels = 2
	Bits     = 16

	period  = 1024
	periods = 4

	full = 32768

	// Measured from the vendor HAL doing the same on a route change.
	codecSettle = 1100 * time.Millisecond
)

const VoiceRate = upsample.VoiceRate

const (
	Card = 0

	AmpSwitch = "Ext_Speaker_Amp_Switch"
)

type Player struct {
	startup atomic.Pointer[sharedcomponent.Progress]
	devMu   sync.Mutex
	pb      *alsa.Playback
	mixer   *alsa.Mixer

	OnOutput hook.Hook[Output]

	volume atomic.Uint32
	step   atomic.Int32

	pathMu        sync.Mutex
	routing       route
	speakerMode   config.OutputMode
	headphoneMode config.OutputMode
	seq           sequences

	voiceMu    sync.Mutex
	voice      upsample.Resampler
	resampling upsample.Kind
	splices    atomic.Uint64

	stereo bool
	chains []*asp.Chain
	lanes  [][]float32
	on     atomic.Bool
	stale  atomic.Bool

	fed bool

	tap    atomic.Pointer[tapTo]
	tapBuf []int16

	underruns atomic.Uint64

	deaf atomic.Uint64

	bus mix.Bus
	sum []int32

	written atomic.Uint64
}

func (p *Player) Attach(s mix.Source) { p.bus.Attach(s) }

func (p *Player) Written() uint64 { return p.written.Load() }

func (p *Player) Latency() time.Duration {
	pb, _ := p.device()
	if pb == nil {
		return 0
	}
	frames, err := pb.Delay()
	if err != nil {
		return 0
	}
	return time.Duration(max(frames, 0)+period) * time.Second / Rate
}

func New() *Player {
	saved := config.Get().Speaker
	p := &Player{
		speakerMode:   saved.SpeakerOutput,
		headphoneMode: saved.HeadphoneOutput,
		stereo:        component.Board().Stereo,
	}
	p.routing = routeFor(pluggedIn(), p.speakerMode, p.headphoneMode)
	p.voice, p.resampling = upsample.New(upsample.Sinc, Rate)
	p.SetVolume(VolumeSteps)
	p.on.Store(config.DefaultASP)

	files := component.Board().Tuning
	if !files.Listed() {
		return p
	}
	t, err := asp.Load(asp.VendorDir, files)
	if err != nil {
		slog.Error("the driver tuning is not available, playing untuned", "err", err)
		return p
	}
	drivers := 1
	if p.stereo {
		drivers = Channels
	}
	for range drivers {
		c, err := t.Chain(period)
		if err != nil {
			slog.Error("the driver tuning will not run, playing untuned", "err", err)
			p.chains, p.lanes = nil, nil
			return p
		}
		p.chains = append(p.chains, c)
		p.lanes = append(p.lanes, make([]float32, period))
	}
	return p
}

var (
	once   sync.Once
	shared *Player
)

func init() {
	// The amplifier hisses when nothing drives the DAC, and toggling it pops.
	component.Register(sharedcomponent.Hardware, Get, sharedcomponent.Order(6),
		sharedcomponent.Supervise(service.Restart(time.Second, 30*time.Second)))
}

func Get() *Player {
	once.Do(func() { shared = New() })
	return shared
}

func (p *Player) Name() string { return "speaker" }

// The first mixer write enumerates the card, which takes over a second.
func (p *Player) open() error {
	m, err := alsa.OpenMixer(Card)
	if err != nil {
		return fmt.Errorf("speaker: opening mixer: %w", err)
	}

	pb, err := alsa.OpenPlayback(Card, PlaybackDevice, alsa.Config{
		Channels:   Channels,
		Rate:       Rate,
		Format:     alsa.FormatS16LE,
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

func (p *Player) device() (*alsa.Playback, *alsa.Mixer) {
	p.devMu.Lock()
	defer p.devMu.Unlock()
	return p.pb, p.mixer
}

func (p *Player) route() {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	p.apply(p.seq.path[p.routing.out])
}

func (p *Player) amp(on bool) {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()

	if !p.routing.speaker {
		return
	}
	if on {
		p.apply(p.seq.on)
		return
	}
	p.apply(p.seq.off)
}

func (p *Player) Output() Output {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	return p.routing.out
}

func (p *Player) SetOutputs(speaker, headphone config.OutputMode) {
	p.pathMu.Lock()
	p.speakerMode, p.headphoneMode = speaker, headphone
	p.pathMu.Unlock()
	p.reroute(pluggedIn())
}

func (p *Player) reroute(plugged bool) {
	p.pathMu.Lock()
	was := p.routing
	want := routeFor(plugged, p.speakerMode, p.headphoneMode)
	if want == was {
		p.pathMu.Unlock()
		return
	}
	p.routing = want
	if was.speaker {
		p.apply(p.seq.off)
	}
	moved := want.out != was.out
	if moved {
		p.apply(p.seq.leave[was.out])
		p.apply(p.seq.path[want.out])
	}
	p.pathMu.Unlock()

	if moved {
		time.Sleep(codecSettle)
	}
	p.amp(true)

	p.SetVolume(int(p.step.Load()))
	slog.Info("output changed", "output", want.out, "speaker", want.speaker)
	if moved {
		p.OnOutput.Emit(want.out)
	}
}

func (p *Player) watchJack(ctx context.Context) {
	t := time.NewTicker(jackPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.reroute(pluggedIn())
		}
	}
}

func (p *Player) choose() {
	_, mixer := p.device()
	if mixer == nil {
		return
	}
	seq := sequencesFor(func(c string) bool {
		_, err := mixer.Find(c)
		return err == nil
	})
	p.pathMu.Lock()
	p.seq = seq
	p.pathMu.Unlock()
}

func (p *Player) sequences() sequences {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	return p.seq
}

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

func (p *Player) Run(ctx context.Context) error {
	pb, _ := p.device()
	if pb == nil {
		return errors.New("speaker: the playback device is not held")
	}
	buf := make([]byte, period*Channels*Bits/8)

	// The vendor HAL's route change order: route with the amplifier off, let the codec idle, enable the amplifier, feed.
	out := p.Output()
	p.choose()
	p.apply(p.sequences().init)
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
		if err := mix.Send(ctx, pb, buf, &p.underruns); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		p.written.Add(period)
	}
}

type tapTo struct{ mix.Tap }

func (p *Player) SetTap(t mix.Tap) {
	if t == nil {
		p.tap.Store(nil)
		return
	}
	p.tap.Store(&tapTo{t})
}

func (p *Player) fill(buf []byte) {
	if cap(p.sum) < period*Channels {
		p.sum = make([]int32, period*Channels)
	}
	mixed := p.sum[:period*Channels]
	take, rendered := p.bus.Next(p.written.Load(), mixed)

	if take > 0 && take < period*Channels {
		p.splices.Add(1)
	}

	speaker := p.Output() == OutputSpeaker
	mono := speaker && !p.stereo

	fed := take > 0 || rendered
	drain := fed || p.fed
	p.fed = fed

	tuned := speaker && len(p.chains) > 0 && p.on.Load() && drain

	tap := p.tap.Load()
	if tap != nil && cap(p.tapBuf) < period*Channels {
		p.tapBuf = make([]int16, period*Channels)
	}

	gain := p.Volume()
	for i, j := 0, 0; i < period*Channels; i, j = i+Channels, j+1 {
		l, r := mixed[i], mixed[i+1]
		if tap != nil {
			p.tapBuf[i], p.tapBuf[i+1] = mix.Clamp(
				int32(float32(l)*gain),
			), mix.Clamp(
				int32(float32(r)*gain),
			)
		}
		if mono {
			l = (l + r) / 2
			r = l
		}
		if tuned {
			p.lanes[0][j] = float32(mix.Clamp(l)) / full
			if p.stereo {
				p.lanes[1][j] = float32(mix.Clamp(r)) / full
			}
			continue
		}
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(float32(mix.Clamp(l))*gain)))
		binary.LittleEndian.PutUint16(buf[(i+1)*2:], uint16(int16(float32(mix.Clamp(r))*gain)))
	}
	if tap != nil {
		tap.Offer(p.tapBuf[:period*Channels])
	}
	if !tuned {
		return
	}

	stale := p.stale.Swap(false)
	share := float64(p.step.Load()) / VolumeSteps
	for c, chain := range p.chains {
		if stale {
			chain.Reset()
		}
		// The tuning's limiter holds a fixed ceiling.
		chain.Process(p.lanes[c], share)
	}
	right := p.lanes[len(p.lanes)-1]
	for i, j := 0, 0; i < period*Channels; i, j = i+Channels, j+1 {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(mix.Clamp(int32(p.lanes[0][j]*full*gain))))
		binary.LittleEndian.PutUint16(buf[(i+1)*2:], uint16(mix.Clamp(int32(right[j]*full*gain))))
	}
}

func (p *Player) Play(samples []int16) { p.PlayStream(config.StreamMedia, samples) }

func (p *Player) PlayStream(s config.Stream, samples []int16) {
	if pb, _ := p.device(); pb == nil {
		if n := p.deaf.Add(1); n == 1 || n%100 == 0 {
			slog.Warn("audio dropped, no playback device", "times", n)
		}
		return
	}

	p.bus.Push(s, samples)
}

func (p *Player) Take() []int16 { return p.bus.Take(config.StreamMedia) }

func (p *Player) Adjust(rewrite func([]int16)) { p.bus.Adjust(config.StreamMedia, rewrite) }

func (p *Player) Heard(s config.Stream, within time.Duration) bool { return p.bus.Heard(s, within) }

func (p *Player) PlayVoice(mono []int16) {
	p.voiceMu.Lock()
	out := p.voice.Run(mono, make([]int16, 0, len(mono)*(Rate/VoiceRate)*Channels))
	p.voiceMu.Unlock()

	p.PlayStream(config.StreamVoice, out)
}

func (p *Player) Drain() {
	p.bus.Clear()

	p.voiceMu.Lock()
	p.voice.Reset()
	p.voiceMu.Unlock()
}

func (p *Player) SetResampling(r config.Resampling) config.Resampling {
	p.voiceMu.Lock()
	defer p.voiceMu.Unlock()

	p.voice, p.resampling = upsample.New(upsample.Kind(r), Rate)
	return config.Resampling(p.resampling)
}

func (p *Player) SetASP(on bool) bool {
	on = on && len(p.chains) > 0
	if on && !p.on.Load() {
		p.stale.Store(true)
	}
	p.on.Store(on)
	return on
}

func (p *Player) ASP() bool { return p.on.Load() }

func (p *Player) Resampling() config.Resampling {
	p.voiceMu.Lock()
	defer p.voiceMu.Unlock()
	return config.Resampling(p.resampling)
}

func (p *Player) Splices() uint64 { return p.splices.Load() }

func (p *Player) Underruns() uint64 { return p.underruns.Load() }

func (p *Player) Clipped() uint64 {
	p.voiceMu.Lock()
	defer p.voiceMu.Unlock()
	return p.voice.Clipped()
}

func (p *Player) Queued() int { return p.bus.Len() / Channels }

func (p *Player) Beep(freq float64, ms int, level float64) {
	p.Chime(config.StreamFeedback, level, tone.Note{Freq: freq, Ms: ms})
}

func (p *Player) Chime(s config.Stream, level float64, notes ...tone.Note) {
	p.Overlay(s, mix.Chime(Rate, Channels, level, notes...))
}

func (p *Player) Overlay(s config.Stream, samples []int16) {
	if pb, _ := p.device(); pb == nil {
		p.PlayStream(s, samples)
		return
	}

	p.bus.Overlay(s, samples)
}

func (p *Player) SetLevel(s config.Stream, level int) { p.bus.SetGain(s, volume.Gain(level)) }

func (p *Player) Mute(s config.Stream, on bool) { p.bus.Mute(s, on) }

func (p *Player) Muted(s config.Stream) bool { return p.bus.Muted(s) }

func (p *Player) SetVolume(step int) {
	step = max(0, min(step, VolumeSteps))
	p.step.Store(int32(step))
	p.volume.Store(math.Float32bits(gainForStep(p.Output(), step)))
}

func (p *Player) Step() int { return int(p.step.Load()) }

func (p *Player) Volume() float32 { return math.Float32frombits(p.volume.Load()) }

func (p *Player) Close() error {
	p.apply(p.sequences().close)

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
