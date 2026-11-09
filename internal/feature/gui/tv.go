//go:build !board_biscuit

package gui

import (
	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/musicapp"
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	libshell "github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/tvapp"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/fetch"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/apps"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	featuretv "github.com/ygelfand/echolocal/internal/feature/tv"
	"github.com/ygelfand/echolocal/internal/feature/volume"
)

func extendTV(look *widgets.UI, o *screens.Options, app func() *screens.App) {
	tv := tvapp.New(tvapp.Options{
		UI:      look,
		Shell:   shell.Get(),
		Guide:   featuretv.Get(),
		Player:  media.Get(),
		Loud:    func() { volume.Get().Show(config.StreamMedia) },
		Client:  fetch.Client(musicapp.FetchTime),
		Gate:    func(w *gogui.Window) gogui.View { return app().Gate(w) },
		Clock24: func() bool { return config.Get().Screen.Hours == schema.TwentyFourHour },
		Redraw:  func() { shell.Get().Redraw() },
	})
	apps.Get().Add(tv.App())
	o.Views = append(o.Views, tv.Screen)
	o.YouTubeTVPage = func() libshell.View { return tv.Page() }
}
