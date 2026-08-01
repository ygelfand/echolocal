package satellite

import (
	"context"
	"log/slog"
	"math"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/alog"
	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/media"
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
	media   *media.Player

	// speaking is set while a reply or an announcement is sounding, which Home Assistant is told is
	// playing: its mapper has no case for announcing and raises on it.
	speaking atomic.Bool

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
				esphome.MediaPlayerFeaturePlayMedia |
				esphome.MediaPlayerFeaturePlay |
				esphome.MediaPlayerFeaturePause |
				esphome.MediaPlayerFeatureStop |
				esphome.MediaPlayerFeatureBrowseMedia |
				esphome.MediaPlayerFeatureAnnounce,
			SupportsPause:    true,
			SupportedFormats: mediaFormat,
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

	if k.Sound != nil && spk != nil {
		p.media = media.New(k.Sound, spk, p.refresh)
	}

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
//
// It runs on the connection's read loop, so nothing here may wait for audio: starting a track hands
// it to a goroutine and returns.
func (p *mediaPlayer) command(c esphome.MediaCommand) {
	if c.HasVolume {
		p.set(int(math.Round(float64(c.Volume) * VolumeSteps)))
	}

	// An announcement is a url too, but a short one at the pipeline's rate, and it interrupts rather
	// than replacing what is playing. It goes through the same path as one from the voice assistant.
	if c.HasMediaURL && c.MediaURL != "" {
		if c.Announcement {
			p.announce(c.MediaURL)
		} else {
			p.media.Play(c.MediaURL)
		}
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
	case esphome.MediaPlayerStop:
		p.media.Stop()
	case esphome.MediaPlayerPause:
		p.media.Pause()
	case esphome.MediaPlayerPlay:
		p.media.Unpause()
	case esphome.MediaPlayerToggle:
		if playing, _ := p.media.Playing(); playing {
			p.media.Pause()
		} else {
			p.media.Unpause()
		}
	}
}

// announce plays a url over whatever is going on, which is what Home Assistant means by one: a
// doorbell or a spoken alert, not a track.
func (p *mediaPlayer) announce(url string) {
	if p.sound == nil {
		return
	}

	p.sounding(true)
	claim := p.sound.Claim("announce", func(ctx context.Context, spk *speaker.Player) error {
		samples, err := fetch(ctx, url)
		if err != nil {
			return err
		}
		spk.PlayVoice(samples)
		spk.PlayVoice(make([]int16, speaker.VoiceRate*replyTail/1000))
		return nil
	})

	// The claim ends once the audio has been heard, not once it has been queued, so this is where
	// the player stops saying it is playing.
	go alog.Safely("announce", func() {
		<-claim.Done()
		p.sounding(false)

		if err := claim.Err(); err != nil {
			slog.Error("playing the announcement failed", "url", url, "err", err)
		}
	})
}

// sounding marks a reply or an announcement as playing, and puts back whatever the player was
// doing once it ends.
func (p *mediaPlayer) sounding(on bool) {
	p.speaking.Store(on)
	p.refresh()
}

// duck and unduck are a turn taking the speaker for as long as it runs, rather than for one sound.
// A conversation is several sounds with listening in between, and music through the middle of it
// would be heard by the microphones as well as by the room.
func (p *mediaPlayer) duck() { p.media.Suspend() }

func (p *mediaPlayer) unduck() { p.media.Resume() }

// refresh tells Home Assistant what the player is doing.
func (p *mediaPlayer) refresh() {
	p.mp.SetState(p.state())
}

func (p *mediaPlayer) state() esphome.MediaPlayerState {
	playing, paused := p.media.Playing()

	switch {
	case playing || p.speaking.Load():
		return esphome.MediaPlayerPlaying
	case paused:
		return esphome.MediaPlayerPaused
	default:
		return esphome.MediaPlayerIdle
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
