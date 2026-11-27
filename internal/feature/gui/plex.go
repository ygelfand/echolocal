//go:build !board_biscuit

package gui

import (
	"github.com/ygelfand/libcountertop/pkg/display/plexapp"
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"

	"github.com/ygelfand/echolocal/internal/feature/apps"
	featureplex "github.com/ygelfand/echolocal/internal/feature/plex"
	"github.com/ygelfand/echolocal/internal/feature/shell"
)

func extendPlex(look *widgets.UI, o *screens.Options, _ func() *screens.App) {
	movies := plexapp.New(plexapp.Options{
		UI:     look,
		Shell:  shell.Get(),
		Plex:   featureplex.Get(),
		Redraw: func() { shell.Get().Redraw() },
	})
	apps.Get().Add(movies.App())
	o.Views = append(o.Views, movies.Screen)
}
