package satellite

import (
	"log/slog"
	"math"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/speaker"
	"github.com/ygelfand/echolocal/internal/state"
)

// VolumeSteps runs 0..30, the range Android gives STREAM_MUSIC and the one the vendor's volume
// curves are indexed by, so a step here is a step there. Home Assistant works in 0..1.
const VolumeSteps = speaker.VolumeSteps

// volumeFlash is how long the ring shows the level after a change.
const volumeFlash = 2 * time.Second

// mediaPlayer is the speaker as Home Assistant sees it: level, mute and playback state in one
// entity. The buttons drive the same thing.
type mediaPlayer struct {
	mp      *esphome.MediaPlayer
	jack    *esphome.BinarySensor
	ring    *ringLight
	speaker *speaker.Player

	step int
}

func newMediaPlayer(ring *ringLight, spk *speaker.Player) *mediaPlayer {
	p := &mediaPlayer{
		mp: &esphome.MediaPlayer{
			Base: esphome.Base{ObjectID: "speaker", Name: "Speaker", Icon: "mdi:speaker"},
			Features: esphome.MediaPlayerFeatureVolumeSet |
				esphome.MediaPlayerFeatureVolumeStep |
				esphome.MediaPlayerFeatureVolumeMute,
		},
		jack: &esphome.BinarySensor{
			Base:        esphome.Base{ObjectID: "headphones", Name: "Headphones", Icon: "mdi:headphones"},
			DeviceClass: "plug",
		},
		ring:    ring,
		speaker: spk,
	}
	p.mp.OnCommand = p.command

	if spk != nil {
		spk.OnOutput = func(out speaker.Output) {
			p.jack.Set(out == speaker.OutputHeadphone)
		}
		p.jack.Set(spk.Output() == speaker.OutputHeadphone)
	}

	p.apply(state.Get().Settings.VolumeOr(VolumeSteps / 2))
	p.mp.SetState(esphome.MediaPlayerIdle)
	return p
}

func (p *mediaPlayer) entities() []esphome.Entity { return []esphome.Entity{p.mp, p.jack} }

// command handles what Home Assistant sends. Volume arrives as a fraction; the buttons and the
// vendor's curves work in steps, so it is rounded to one.
func (p *mediaPlayer) command(c esphome.MediaCommand) {
	if c.HasVolume {
		p.set(int(math.Round(float64(c.Volume) * VolumeSteps)))
	}
	if !c.HasCommand {
		return
	}

	switch c.Command {
	case esphome.MediaPlayerVolumeUp:
		p.adjust(1)
	case esphome.MediaPlayerVolumeDown:
		p.adjust(-1)
	case esphome.MediaPlayerMute:
		p.mute(true)
	case esphome.MediaPlayerUnmute:
		p.mute(false)
	case esphome.MediaPlayerStop, esphome.MediaPlayerPause:
		p.mp.SetState(esphome.MediaPlayerIdle)
	case esphome.MediaPlayerPlay:
		p.mp.SetState(esphome.MediaPlayerPlaying)
	}
}

// set applies a level and remembers it.
func (p *mediaPlayer) set(step int) {
	applied := p.apply(step)
	if err := state.SetVolume(applied); err != nil {
		slog.Error("saving volume failed", "err", err)
	}
}

// apply drives the speaker and the ring, and reports the step it settled on.
func (p *mediaPlayer) apply(step int) int {
	step = max(0, min(step, VolumeSteps))
	p.step = step

	p.mp.SetVolume(float32(step) / VolumeSteps)
	p.show(step)
	if p.speaker != nil {
		p.speaker.SetVolume(step)
	}
	slog.Info("volume", "step", step, "of", VolumeSteps)
	return step
}

// mute drops the output without losing the level it was at.
func (p *mediaPlayer) mute(muted bool) {
	p.mp.SetMuted(muted)
	if p.speaker == nil {
		return
	}
	if muted {
		p.speaker.SetVolume(0)
		return
	}
	p.speaker.SetVolume(p.step)
}

func (p *mediaPlayer) adjust(delta int) {
	p.set(p.step + delta)
	chime(p.speaker, toneVolume)
}

// show lights the level as a clockwise arc, the leading segment dimmed by the fraction of a
// segment the level does not fill.
func (p *mediaPlayer) show(step int) {
	frame := led.Arc(float64(step)/VolumeSteps, led.Color{R: 0xFF, G: 0xFF, B: 0xFF})
	p.ring.Flash(frame, volumeFlash)
}
