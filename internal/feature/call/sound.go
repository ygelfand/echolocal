package call

import (
	"context"

	"github.com/ygelfand/libcountertop/pkg/audio/tone"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

type device struct{}

func (device) Frames() (<-chan []int16, func()) { return mic.Get().Listen("call") }

func (device) Play(pcm []int16) {
	speaker.Get().PlayStream(config.StreamVoice, pcm)
}

func (device) Queued() int { return speaker.Get().Lane(config.StreamVoice).Queued() }

func (device) Claim(name string, run func(context.Context) error) {
	speaker.Sound().
		Claim(name, func(ctx context.Context, _ *speaker.Player) error { return run(ctx) })
}
func (device) Duck(on bool) { speaker.Sound().Backgrounds().Duck(on) }
func (device) Tone(s config.Stream, level float64, snd tone.Sound) {
	speaker.Sound().Sound(s, level, snd)
}
