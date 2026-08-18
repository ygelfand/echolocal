// Package media is the speaker as Home Assistant sees it: one media_player entity carrying the
// level, the mute and what is playing, plus the playback behind it.
//
// A turn takes the speaker away from a track and gives it back, so music through the middle of a
// conversation is heard by neither the room nor the microphones.
package media

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/buttons"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/lib/noise"
	"github.com/ygelfand/echolocal/internal/lib/safe"
)

func init() {
	component.Register(component.Device, Get(), component.Order(25))
}

// VolumeSteps runs 0..30, the range Android gives STREAM_MUSIC and the one the vendor's volume
// curves are indexed by, so a step here is a step there. Home Assistant works in 0..1.
const VolumeSteps = speaker.VolumeSteps

// volumeFlash is how long the ring shows the level after a change.
const volumeFlash = 2 * time.Second

// turnVolumeSettle keeps Home Assistant's assistant-volume restore out of the ring after the turn
// itself closes. Those commands are transport housekeeping, not volume changes somebody made.
const turnVolumeSettle = 3 * time.Second

type Player struct {
	mp     *esphome.MediaPlayer
	jack   *esphome.BinarySensor
	stream *Stream

	// resampling is how voice is stretched to the playback rate. A reply arrives at the pipeline's
	// rate and the speaker runs at the codec's, so something has to bridge them and which filter does
	// it is audible.
	resampling *esphome.Select
	onTurn     *esphome.Select
	duck       *esphome.Number

	// layers are sounds the device makes on its own, for as long as they are left set. More than one,
	// because a bed with a texture over it — crickets under wind — is worth having and the native API
	// has no entity that holds more than one value.
	layers []*esphome.Select

	// speaking is set while a reply or an announcement is sounding, which Home Assistant is told is
	// playing: its mapper has no case for announcing and raises on it.
	speaking atomic.Bool

	external atomic.Bool

	// volumeQuietUntil is the end of the interval in which volume commands arriving from Home
	// Assistant belong to a voice turn. MaxInt64 means the turn is still open. Physical controls do
	// not consult it: their volume arc is real feedback and must remain visible.
	volumeQuietUntil atomic.Int64

	step int
}

var (
	once   sync.Once
	shared *Player
)

func Get() *Player {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Player {
	p := &Player{
		mp: &esphome.MediaPlayer{
			Base: esphome.Base{ObjectID: "speaker", Name: "Speaker", Icon: "mdi:speaker"},
			Features: esphome.MediaPlayerFeatureVolumeSet |
				esphome.MediaPlayerFeatureVolumeStep |
				esphome.MediaPlayerFeatureVolumeMute |
				esphome.MediaPlayerFeaturePlayMedia |
				esphome.MediaPlayerFeaturePlay |
				esphome.MediaPlayerFeaturePause |
				esphome.MediaPlayerFeatureStop |
				esphome.MediaPlayerFeatureBrowseMedia |
				esphome.MediaPlayerFeatureAnnounce,
			SupportsPause:    true,
			SupportedFormats: Formats,
		},
		jack: &esphome.BinarySensor{
			Base: esphome.Base{
				ObjectID: "headphones",
				Name:     "Headphones",
				Icon:     "mdi:headphones",
				Category: esphome.CategoryDiagnostic,
			},
			DeviceClass: "plug",
		},
		resampling: &esphome.Select{
			Base: esphome.Base{
				ObjectID: "voice_resampling",
				Name:     "Voice resampling",
				Icon:     "mdi:sine-wave",
				Category: esphome.CategoryConfig,
			},
		},
		onTurn: &esphome.Select{
			Base: esphome.Base{
				ObjectID: "media_on_turn",
				Name:     "Music during a turn",
				Icon:     "mdi:music-note-eighth",
				Category: esphome.CategoryConfig,
			},
		},
		duck: &esphome.Number{
			Base: esphome.Base{
				ObjectID: "media_duck_level",
				Name:     "Music ducking",
				Icon:     "mdi:volume-medium",
				Category: esphome.CategoryConfig,
			},
			Min: -40, Max: -3, Step: 1, Unit: "dB",
			Mode: esphome.NumberBox,
		},
	}
	p.layers = noiseLayers()

	// The player itself stays on the device: it is what people reach for. These are how it behaves.
	bases := []*esphome.Base{&p.resampling.Base, &p.onTurn.Base, &p.duck.Base, &p.jack.Base}
	for _, sel := range p.layers {
		bases = append(bases, &sel.Base)
	}
	for _, b := range bases {
		b.DeviceID = component.DevicePlayback
	}

	p.mp.OnCommand = p.command
	component.Bind(p.resampling, speaker.Resamplings(), speaker.Get().SetResampling,
		config.Set().Speaker().Resampling)

	// Nothing to apply: the stream reads the setting when a turn begins, so changing it takes effect on
	// the next one rather than in the middle of this one.
	component.Bind(p.onTurn, onTurns(), func(v config.OnTurn) config.OnTurn { return v },
		config.Set().Media().OnTurn)

	// Not saved and not restored: it is a sound somebody asked for, and a device that came back from an
	// update hissing in a dark room would be a fault as far as anyone in it is concerned.
	for _, sel := range p.layers {
		sel.OnCommand = func(chosen string) {
			if chosen != noiseOff && !noise.Has(chosen) {
				slog.Warn("unknown option", "setting", sel.ObjectID, "value", chosen)
				return
			}

			sel.Set(chosen)
			p.sound()
		}
	}

	p.duck.OnCommand = func(v float32) {
		p.duck.Set(v)
		if err := config.Set().Media().DuckDB(int(v)); err != nil {
			slog.Error("saving the ducking level failed", "err", err)
		}
	}
	p.stream = NewStream(speaker.Sound(), speaker.Get(), p.refresh)

	// Volume acts on every tap and on every repeat, so a held button ramps.
	buttons.Get().Events.Listen(func(e buttons.Event) {
		if e.Kind == buttons.Hold {
			return
		}
		switch e.Name {
		case buttons.VolumeUp:
			p.Adjust(1)
		case buttons.VolumeDown:
			p.Adjust(-1)
		}
	})

	spk := speaker.Get()
	spk.OnOutput.Listen(func(out speaker.Output) { p.jack.Set(out == speaker.OutputHeadphone) })
	p.jack.Set(spk.Output() == speaker.OutputHeadphone)

	p.mp.SetState(esphome.MediaPlayerIdle)
	for _, sel := range p.layers {
		sel.Set(noiseOff)
	}
	return p
}

func (p *Player) Name() string { return "media player" }

func (p *Player) Entities() []esphome.Entity {
	out := []esphome.Entity{p.mp, p.jack, p.resampling, p.onTurn, p.duck}
	for _, sel := range p.layers {
		out = append(out, sel)
	}
	return out
}

// Restore puts the volume back where it was, without flashing the arc: nothing happened, the device
// is starting where it left off.
func (p *Player) Restore(c config.Config) {
	p.apply(c.Speaker.Volume, false)
	slog.Info("restored", "what", "volume", "step", c.Speaker.Volume, "of", VolumeSteps)

	component.Restore(p.resampling, c.Speaker.Resampling, speaker.Get().SetResampling)

	component.Restore(p.onTurn, c.Media.OnTurn, func(v config.OnTurn) config.OnTurn { return v })

	p.duck.Set(float32(c.Media.DuckDB))
	slog.Info("restored", "what", p.duck.ObjectID, "using", c.Media.DuckDB)
}

// onTurns is what music may do about a turn.
func onTurns() []config.OnTurn { return []config.OnTurn{config.OnTurnDuck, config.OnTurnPause} }

// noiseOff is the way out of the list, and what the entities read whenever the speaker is doing
// anything else.
const noiseOff = "None"

// noiseLayers are the slots a sound can be put in. They are peers: any sound in any slot, one on its
// own or both mixed.
func noiseLayers() []*esphome.Select {
	const slots = 2

	out := make([]*esphome.Select, 0, slots)
	for i := 1; i <= slots; i++ {
		out = append(out, &esphome.Select{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("noise_layer_%d", i),
				Name:     fmt.Sprintf("White noise layer %d", i),
				Icon:     "mdi:blur",
			},
			Options: append([]string{noiseOff}, noise.Names()...),
		})
	}
	return out
}

// sound starts whatever the slots add up to, and stops when they add up to nothing.
func (p *Player) sound() {
	var sounds []string
	for _, sel := range p.layers {
		if chosen := sel.Get(); chosen != noiseOff && chosen != "" {
			sounds = append(sounds, chosen)
		}
	}

	if len(sounds) == 0 {
		p.stream.Stop()
		return
	}
	p.stream.PlayNoise(sounds...)
}

// command handles what Home Assistant sends. Volume arrives as a fraction; the buttons and the
// vendor's curves work in steps, so it is rounded to one.
//
// It runs on the connection's read loop, so nothing here may wait for audio: starting a track hands
// it to a goroutine and returns.
func (p *Player) command(c esphome.MediaCommand) {
	if c.HasVolume {
		p.set(int(math.Round(float64(c.Volume)*VolumeSteps)), p.volumeFeedback(time.Now()))
	}

	// An announcement is a url too, but a short one at the pipeline's rate, and it interrupts rather
	// than replacing what is playing. It goes through the same path as one from the voice assistant.
	if c.HasMediaURL && c.MediaURL != "" {
		if c.Announcement {
			p.announce(c.MediaURL)
		} else {
			p.stream.Play(c.MediaURL)
		}
	}
	if !c.HasCommand {
		return
	}

	switch c.Command {
	case esphome.MediaPlayerVolumeUp:
		p.Adjust(1)
	case esphome.MediaPlayerVolumeDown:
		p.Adjust(-1)
	case esphome.MediaPlayerMute:
		p.Mute(true)
	case esphome.MediaPlayerUnmute:
		p.Mute(false)
	case esphome.MediaPlayerStop:
		p.stream.Stop()
	case esphome.MediaPlayerPause:
		p.stream.Pause()
	case esphome.MediaPlayerPlay:
		p.stream.Unpause()
	case esphome.MediaPlayerToggle:
		if playing, _ := p.stream.Playing(); playing {
			p.stream.Pause()
		} else {
			p.stream.Unpause()
		}
	}
}

// announce plays a url over whatever is going on, which is what Home Assistant means by one: a
// doorbell or a spoken alert, not a track.
func (p *Player) announce(url string) {
	p.Sounding(true)
	claim := speaker.Sound().Claim("announce", func(ctx context.Context, spk *speaker.Player) error {
		samples, err := Fetch(ctx, url)
		if err != nil {
			return err
		}
		spk.PlayVoice(samples)
		spk.PlayVoice(make([]int16, speaker.VoiceRate*Tail/1000))
		return nil
	})

	// The claim ends once the audio has been heard, not once it has been queued, so this is where
	// the player stops saying it is playing.
	safe.Go("announce", func() {
		<-claim.Done()
		p.Sounding(false)

		if err := claim.Err(); err != nil {
			slog.Error("playing the announcement failed", "url", url, "err", err)
		}
	})
}

// Sounding marks a reply or an announcement as playing, and puts back whatever the player was doing
// once it ends.
func (p *Player) Sounding(on bool) {
	p.speaking.Store(on)
	p.refresh()
}

// External marks the speaker as busy with something this player did not start.
func (p *Player) External(on bool) {
	p.external.Store(on)
	p.refresh()
}

// Playing reports what the track is doing, which is what decides whether a turn has anything to take
// the speaker from.
func (p *Player) Playing() (playing, paused bool) { return p.stream.Playing() }

// Pause leaves the track where it is, so it can be picked up again.
func (p *Player) Pause() { p.stream.Pause() }

// refresh tells Home Assistant what the player is doing. Anything that displaces the noise — a track,
// a stop, the action button — clears both entities, rather than leaving them naming a sound nobody can
// hear.
func (p *Player) refresh() {
	p.mp.SetState(p.state())

	if len(p.stream.Noise()) == 0 {
		for _, sel := range p.layers {
			sel.Set(noiseOff)
		}
	}
}

func (p *Player) state() esphome.MediaPlayerState {
	playing, paused := p.stream.Playing()

	switch {
	case playing || p.speaking.Load() || p.external.Load():
		return esphome.MediaPlayerPlaying
	case paused:
		return esphome.MediaPlayerPaused
	default:
		return esphome.MediaPlayerIdle
	}
}

// Set applies a level and remembers it.
func (p *Player) Set(step int) {
	p.set(step, true)
}

// set applies and remembers a level. tell controls only user-facing feedback; assistant-managed
// volume still has to reach the speaker and Home Assistant while its arc stays off the ring.
func (p *Player) set(step int, tell bool) {
	applied := p.apply(step, tell)
	if err := config.Set().Speaker().Volume(applied); err != nil {
		slog.Error("saving volume failed", "err", err)
	}
}

// VoiceTurn marks the interval in which Home Assistant may temporarily move the media player's
// volume for an assistant response. The short tail includes its restore commands, which can arrive
// just after the conversation pipeline reports that the turn is over.
func (p *Player) VoiceTurn(on bool) {
	until := time.Now().Add(turnVolumeSettle).UnixNano()
	if on {
		until = math.MaxInt64
	}
	p.volumeQuietUntil.Store(until)
}

func (p *Player) volumeFeedback(at time.Time) bool {
	return at.UnixNano() > p.volumeQuietUntil.Load()
}

// apply drives the speaker and reports the step it settled on. tell is false when nothing happened that
// anyone needs to see or read about, which is a restore: the arc is a response to being turned up, not a
// readout of the current level.
func (p *Player) apply(step int, tell bool) int {
	step = max(0, min(step, VolumeSteps))
	p.step = step

	p.mp.SetVolume(float32(step) / VolumeSteps)
	speaker.Get().SetVolume(step)
	if !tell {
		return step
	}

	p.show(step)
	slog.Info("volume", "step", step, "of", VolumeSteps)
	return step
}

// Mute drops the output without losing the level it was at.
func (p *Player) Mute(muted bool) {
	p.mp.SetMuted(muted)
	if muted {
		speaker.Get().SetVolume(0)
		return
	}
	speaker.Get().SetVolume(p.step)
}

// Adjust moves the level by a step and says so, which is what the buttons and Home Assistant's own
// up and down both do.
func (p *Player) Adjust(delta int) {
	p.Set(p.step + delta)
	speaker.Sound().Chime(speaker.ToneVolume)
}

// show lights the level as a clockwise arc, the leading segment dimmed by the fraction of a segment
// the level does not fill. It takes its own claim each time and lets it expire, which is what puts
// back whatever was underneath — including a conversation that is still running.
func (p *Player) show(step int) {
	frame := led.Volume(float64(step) / VolumeSteps)
	led.Get().Claim(led.PriorityNotice).PaintFor(frame, volumeFlash)
}
