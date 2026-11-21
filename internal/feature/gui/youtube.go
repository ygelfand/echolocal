//go:build !board_biscuit

package gui

import (
	"github.com/ygelfand/libcountertop/pkg/audio/resample"
	"github.com/ygelfand/libcountertop/pkg/display/musicapp"
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	"github.com/ygelfand/libcountertop/pkg/display/video"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/display/youtubeapp"
	"github.com/ygelfand/libcountertop/pkg/fetch"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
	"github.com/ygelfand/libcountertop/pkg/media/youtube"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/apps"
	"github.com/ygelfand/echolocal/internal/feature/playout"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/hardware/display"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

func extendYouTube(look *widgets.UI, o *screens.Options, _ func() *screens.App) {
	client := fetch.Client(musicapp.FetchTime)
	r := youtube.NewResolver(client, func() playback.Target {
		return video.Target(display.Get(), component.Board().MaxFPS)
	})
	r.Resample = func(from int) func([]int16) []int16 {
		return resample.NewRational(from, speaker.Rate, speaker.Channels).Run
	}
	yt := youtubeapp.New(youtubeapp.Options{
		UI:       look,
		Shell:    shell.Get(),
		Accounts: config.YouTubeAccountsSection,
		Resolver: r,
		Output:   playout.New(),
		Skip:     func() []string { return config.Get().Cast.YouTube.Skip },
		Autoplay: func() bool { return config.Get().Media.Autoplay },
		Client:   client,
		Redraw:   func() { shell.Get().Redraw() },
	})
	apps.Get().Add(yt.App())
	o.Views = append(o.Views, yt.Screen)
}
