// Package media is the speaker as Home Assistant sees it: one media_player entity carrying the
// level, the mute and what is playing, plus the playback behind it.
//
// A turn takes the speaker away from a track and gives it back, so music through the middle of a
// conversation is heard by neither the room nor the microphones.
package media

import (
	"context"
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

	// speaking is set while a reply or an announcement is sounding, which Home Assistant is told is
	// playing: its mapper has no case for announcing and raises on it.
	speaking atomic.Bool

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
	p.mp.OnCommand = p.command
	component.Bind(p.resampling, speaker.Resamplings(), speaker.Get().SetResampling,
		config.Set().Speaker().Resampling)

	// Nothing to apply: the stream reads the setting when a turn begins, so changing it takes effect on
	// the next one rather than in the middle of this one.
	component.Bind(p.onTurn, onTurns(), func(v config.OnTurn) config.OnTurn { return v },
		config.Set().Media().OnTurn)

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
	return p
}

func (p *Player) Name() string { return "media player" }

func (p *Player) Entities() []esphome.Entity {
	return []esphome.Entity{p.mp, p.jack, p.resampling, p.onTurn, p.duck}
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

// command handles what Home Assistant sends. Volume arrives as a fraction; the buttons and the
// vendor's curves work in steps, so it is rounded to one.
//
// It runs on the connection's read loop, so nothing here may wait for audio: starting a track hands
// it to a goroutine and returns.
func (p *Player) command(c esphome.MediaCommand) {
	if c.HasVolume {
		p.Set(int(math.Round(float64(c.Volume) * VolumeSteps)))
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

// Duck and Unduck are a turn taking the speaker for as long as it runs, rather than for one sound. A
// conversation is several sounds with listening in between, and music through the middle of it would
// be heard by the microphones as well as by the room.
//
// Whether that means lowering the track or stopping it is the stream's decision, from configuration:
// stopping it leaves a hole in the middle of a song for the sake of one question.
func (p *Player) Duck() { p.stream.Duck(true) }

func (p *Player) Unduck() { p.stream.Duck(false) }

// Playing reports what the track is doing, which is what decides whether a turn has anything to take
// the speaker from.
func (p *Player) Playing() (playing, paused bool) { return p.stream.Playing() }

// Pause leaves the track where it is, so it can be picked up again.
func (p *Player) Pause() { p.stream.Pause() }

// refresh tells Home Assistant what the player is doing.
func (p *Player) refresh() { p.mp.SetState(p.state()) }

func (p *Player) state() esphome.MediaPlayerState {
	playing, paused := p.stream.Playing()

	switch {
	case playing || p.speaking.Load():
		return esphome.MediaPlayerPlaying
	case paused:
		return esphome.MediaPlayerPaused
	default:
		return esphome.MediaPlayerIdle
	}
}

// Set applies a level and remembers it.
func (p *Player) Set(step int) {
	applied := p.apply(step, true)
	if err := config.Set().Speaker().Volume(applied); err != nil {
		slog.Error("saving volume failed", "err", err)
	}
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
