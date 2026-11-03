//go:build !board_biscuit

package gui

import (
	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/musicapp"
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	sharedsettings "github.com/ygelfand/libcountertop/pkg/display/settings"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/fetch"
	"github.com/ygelfand/libcountertop/pkg/homeassistant/musicassistant"

	"github.com/ygelfand/echolocal/internal/feature/apps"
	"github.com/ygelfand/echolocal/internal/feature/cameras"
	"github.com/ygelfand/echolocal/internal/feature/discovery"
	"github.com/ygelfand/echolocal/internal/feature/product"
	"github.com/ygelfand/echolocal/internal/feature/sendspin"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/ui"
)

func extendMusic(look *widgets.UI, o *screens.Options, app func() *screens.App) {
	music := musicapp.New(musicapp.Options{
		UI:     look,
		Shell:  shell.Get(),
		Music:  musicassistant.BuildMusic(product.Get(), sendspin.ClientID),
		Peers:  discovery.Get(),
		Logo:   sharedsettings.Themed(ui.Logo().Light, ui.Logo().Dark),
		Site:   cameras.Site().URL,
		Client: fetch.Client(musicapp.FetchTime),
		Gate:   func(w *gogui.Window) gogui.View { return app().Gate(w) },
		Redraw: func() { shell.Get().Redraw() },
	})
	apps.Get().Add(music.App())
	o.Views = append(o.Views, music.Screen)
}
