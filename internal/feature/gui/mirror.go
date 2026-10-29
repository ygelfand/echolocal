//go:build !board_biscuit

package gui

import (
	"github.com/ygelfand/libcountertop/pkg/display/mirror"
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/feature/apps"
	"github.com/ygelfand/echolocal/internal/feature/commands"
	"github.com/ygelfand/echolocal/internal/feature/settings"
	"github.com/ygelfand/echolocal/internal/feature/shell"
)

func extendMirror(look *widgets.UI, o *screens.Options, _ func() *screens.App) {
	if component.Board().CameraWidth == 0 {
		return
	}
	m := mirror.New(mirror.Options{Shell: shell.Get(), Camera: settings.Preview()})
	apps.Get().Add(m.App())
	commands.Get().Listen(m.Command())
	o.Views = append(o.Views, m.Screen)
}
