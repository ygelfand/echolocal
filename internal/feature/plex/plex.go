//go:build !board_biscuit

package plex

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/audio/resample"
	"github.com/ygelfand/libcountertop/pkg/display/video"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
	"github.com/ygelfand/libcountertop/pkg/media/plex"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/playout"
	"github.com/ygelfand/echolocal/internal/feature/product"
	"github.com/ygelfand/echolocal/internal/hardware/display"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

var get = sync.OnceValue(func() *plex.Service {
	return plex.Build(product.Get(), config.PlexSection, plex.PlayerOptions{
		Output: playout.New(),
		Target: func() playback.Target {
			return video.Target(display.Get(), component.Board().MaxFPS)
		},
		Decoder: plex.Decoder{
			Width:  component.Board().DecodeWidth,
			Height: component.Board().DecodeHeight,
		},
		Resample: func(from int) func([]int16) []int16 {
			return resample.NewRational(from, speaker.Rate, speaker.Channels).Run
		},
	}, config.Get().Device.Name)
})

func Get() *plex.Service { return get() }
