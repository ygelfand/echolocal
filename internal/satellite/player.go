package satellite

import (
	"log/slog"
	"math"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/speaker"
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
	leds    *led.Driver
	speaker *speaker.Player
	sound   *speaker.Driver

	step int
}

func newMediaPlayer(k *kit) *mediaPlayer {
	leds, spk := k.LEDs, k.Speaker
	p := &mediaPlayer{
		mp: &esphome.MediaPlayer{
			Base: esphome.Base{ObjectID: "speaker", Name: "Speaker", Icon: "mdi:speaker"},
			Features: esphome.MediaPlayerFeatureVolumeSet |
				esphome.MediaPlayerFeatureVolumeStep |
				esphome.MediaPlayerFeatureVolumeMute |
				esphome.MediaPlayerFeatureAnnounce,
			SupportedFormats: announceFormat,
		},
		jack: &esphome.BinarySensor{
			Base:        esphome.Base{ObjectID: "headphones", Name: "Headphones", Icon: "mdi:headphones", Category: esphome.CategoryDiagnostic},
			DeviceClass: "plug",
		},
		leds:    leds,
		speaker: spk,
		sound:   k.Sound,
	}
	p.mp.OnCommand = p.command

	if spk != nil {
		spk.OnOutput = func(out speaker.Output) {
			p.jack.Set(out == speaker.OutputHeadphone)
		}
		p.jack.Set(spk.Output() == speaker.OutputHeadphone)
	}

	p.mp.SetState(esphome.MediaPlayerIdle)
	return p
}

// restore puts the volume back where it was, without flashing the arc: nothing happened, the device is
// starting where it left off.
func (p *mediaPlayer) restore(saved settings.Stored) {
	step := saved.Speaker.VolumeOr(VolumeSteps / 2)
	p.apply(step, false)
	slog.Info("restored", "what", "volume", "step", step, "of", VolumeSteps,
		"from", from(saved.Speaker.Volume != nil))
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
	applied := p.apply(step, true)
	if err := settings.SetSpeakerVolume(applied); err != nil {
		slog.Error("saving volume failed", "err", err)
	}
}

// apply drives the speaker and reports the step it settled on. tell is false when nothing happened that
// anyone needs to see or read about, which is a restore: the arc is a response to being turned up, not a
// readout of the current level.
func (p *mediaPlayer) apply(step int, tell bool) int {
	step = max(0, min(step, VolumeSteps))
	p.step = step

	p.mp.SetVolume(float32(step) / VolumeSteps)
	if p.speaker != nil {
		p.speaker.SetVolume(step)
	}
	if !tell {
		return step
	}

	p.show(step)
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
	chime(p.sound, toneVolume)
}

// show lights the level as a clockwise arc, the leading segment dimmed by the fraction of a segment
// the level does not fill. It takes its own claim each time and lets it expire, which is what puts
// back whatever was underneath — including a conversation that is still running.
func (p *mediaPlayer) show(step int) {
	frame := led.Volume(float64(step) / VolumeSteps)
	p.leds.Claim(led.PriorityNotice).PaintFor(frame, volumeFlash)
}
