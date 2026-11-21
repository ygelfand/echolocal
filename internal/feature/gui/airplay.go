//go:build !board_biscuit

package gui

import (
	"github.com/ygelfand/libcountertop/pkg/display/airplayview"
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"

	"github.com/ygelfand/echolocal/internal/feature/airplay"
	"github.com/ygelfand/echolocal/internal/feature/shell"
)

func extendAirPlay(look *widgets.UI, o *screens.Options, _ func() *screens.App) {
	codes := airplayview.New(airplayview.Options{UI: look, Shell: shell.Get()})
	airplay.Get().ShowCodes(codes.Show)
	o.Views = append(o.Views, codes.Screen)
}
