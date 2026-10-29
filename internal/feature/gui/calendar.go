//go:build !board_biscuit

package gui

import (
	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/agenda"
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/apps"
	"github.com/ygelfand/echolocal/internal/feature/calendar"
	"github.com/ygelfand/echolocal/internal/feature/shell"
)

func extendCalendar(look *widgets.UI, o *screens.Options, app func() *screens.App) {
	cal := calendar.Get().Calendar
	diary := agenda.New(agenda.Options{
		UI:       look,
		Shell:    shell.Get(),
		Calendar: cal,
		Gate:     func(w *gogui.Window) gogui.View { return app().Gate(w) },
		TwentyFour: func() bool {
			return config.Get().Screen.Hours == schema.TwentyFourHour
		},
	})
	apps.Get().Add(diary.App())
	o.Calendar = cal
	o.Views = append(o.Views, diary.Screen)
}
