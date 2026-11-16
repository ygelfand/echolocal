package voice

import (
	"context"

	"github.com/ygelfand/libcountertop/pkg/assistant/turn"
	"github.com/ygelfand/libcountertop/pkg/audio/sound"
	sharedwake "github.com/ygelfand/libcountertop/pkg/inference/wake"
	"github.com/ygelfand/libcountertop/pkg/inference/wakeslots"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/activity"
	"github.com/ygelfand/echolocal/internal/feature/arbitration"
	"github.com/ygelfand/echolocal/internal/feature/feedback"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/mute"
	"github.com/ygelfand/echolocal/internal/feature/recording"
	"github.com/ygelfand/echolocal/internal/feature/tones"
	"github.com/ygelfand/echolocal/internal/feature/voicematch"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/lib/wake"
)

type voiceSound struct{ *speaker.Driver }

func (s voiceSound) Claim(name string, fn func(context.Context, turn.Speaker) error) turn.Claim {
	return s.Driver.Claim(
		name,
		func(ctx context.Context, p *speaker.Player) error { return fn(ctx, p) },
	)
}
func (s voiceSound) Duck(on bool) { s.Backgrounds().Duck(on) }

func dependencies() turn.Dependencies {
	return turn.Dependencies{
		Source:       mic.Get(),
		Speaker:      speaker.Get(),
		Sound:        voiceSound{speaker.Sound()},
		Player:       media.Get(),
		Log:          activity.Get(),
		Recorder:     recording.Get(),
		Models:       func() []sharedwake.Model { return wake.Lib().Ours() },
		HardwareTail: sound.HardwareTail,
		Failure:      feedback.Failure,
		Cancelled:    feedback.Cancelled,
		Words: turn.Words{
			Settings: config.WakeSection,
			Speaker:  speaker.Sound(),
			Sounds:   tones.Get(),
		},
		Muted: mute.Get().Muted,
		Presentation: &turn.Presentation{
			Settings: config.WakeSection,
			Shown:    &Shown,
			Effects:  &turnEffects{},
		},
		Arbiter: arbitration.Get(),
		Voices:  voicematch.Get(),
	}
}

func wanted(models []sharedwake.Model, count int) []string {
	saved := config.Get().Wake.Words
	var ids []string
	if saved != nil {
		ids = make([]string, len(saved))
		for i, word := range saved {
			ids[i] = word.ID
		}
	}
	installed := make([]string, len(models))
	for i, m := range models {
		installed[i] = m.ID
	}
	return wakeslots.Initial(ids, installed, sharedwake.DefaultModel, count)
}
