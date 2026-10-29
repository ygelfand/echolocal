//go:build !board_biscuit

package gui

import (
	sharedvolume "github.com/ygelfand/libcountertop/pkg/audio/volume"
	"github.com/ygelfand/libcountertop/pkg/display/callview"
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/apps"
	"github.com/ygelfand/echolocal/internal/feature/call"
	"github.com/ygelfand/echolocal/internal/feature/discovery"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/feature/volume"
)

func extendCalls(look *widgets.UI, o *screens.Options, _ func() *screens.App) {
	calls := callview.New(callview.Options{
		UI:    look,
		Shell: shell.Get(),
		Calls: call.Get(),
		Peers: discovery.Get(),
		Voice: sharedvolume.For(volume.Get(), config.StreamVoice),
		Video: component.Board().CameraWidth > 0,
	})
	apps.Get().Add(calls.App())
	discovery.Get().Changed.Listen(func(struct{}) { shell.Get().Redraw() })
	o.Views = append(o.Views, calls.Screen)
}
