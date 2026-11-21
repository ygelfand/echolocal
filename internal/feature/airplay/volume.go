package airplay

import (
	"github.com/ygelfand/libcountertop/pkg/audio/volume"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/media"
)

type mediaVolume struct{}

func (mediaVolume) Level() int { return media.Get().Levels.Level(config.StreamMedia) }

func (mediaVolume) SetLevel(level int) {
	media.Get().Levels.Set(config.StreamMedia, volume.Clamp(level))
}

func (mediaVolume) Mute(on bool) { media.Get().Levels.Mute(config.StreamMedia, on) }

func (mediaVolume) Muted() bool { return media.Get().Levels.Muted(config.StreamMedia) }

func (mediaVolume) Listen(changed func(int)) func() {
	return media.Get().Levels.Watch(config.StreamMedia, changed)
}
