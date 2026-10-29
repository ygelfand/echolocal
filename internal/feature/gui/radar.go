//go:build !board_biscuit

package gui

import (
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/radarview"
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/apps"
	"github.com/ygelfand/echolocal/internal/feature/commands"
	"github.com/ygelfand/echolocal/internal/feature/radar"
	"github.com/ygelfand/echolocal/internal/feature/shell"
)

func extendRadar(look *widgets.UI, o *screens.Options, app func() *screens.App) {
	sky := radarview.New(radarview.Options{
		UI:       look,
		Shell:    shell.Get(),
		Radar:    radar.Get(),
		Settings: config.WeatherSection,
		Clock24: func() bool {
			return config.Get().Screen.Hours == schema.TwentyFourHour
		},
		Again: func(w *gogui.Window, after time.Duration) { app().Again(w, after) },
		Gate:  func(w *gogui.Window) gogui.View { return app().Gate(w) },
	})
	apps.Get().Add(sky.App())
	commands.Get().Listen(sky.Command())
	o.OpenRadar = sky.Open
	o.Views = append(o.Views, sky.Screen)
}
