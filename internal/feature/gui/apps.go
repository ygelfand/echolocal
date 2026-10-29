//go:build !board_biscuit

package gui

import (
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"

	"github.com/ygelfand/echolocal/internal/feature/apps"
)

func extendApps(look *widgets.UI, o *screens.Options, _ func() *screens.App) {
	o.Views = append(o.Views, apps.Get().Screens(look))
}
