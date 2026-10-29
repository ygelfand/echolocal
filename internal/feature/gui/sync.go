//go:build !board_biscuit

package gui

import (
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	sharedsettings "github.com/ygelfand/libcountertop/pkg/display/settings"
	"github.com/ygelfand/libcountertop/pkg/display/syncview"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"

	"github.com/ygelfand/echolocal/internal/feature/configsync"
	"github.com/ygelfand/echolocal/internal/feature/discovery"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/ui"
)

func extendSync(look *widgets.UI, o *screens.Options, _ func() *screens.App) {
	syncs := syncview.New(syncview.Options{
		UI:    look,
		Shell: shell.Get(),
		Sync:  configsync.Get(),
		Peers: discovery.Get(),
		Logo:  sharedsettings.Themed(ui.Logo().Light, ui.Logo().Dark),
	})
	o.Views = append(o.Views, syncs.Screen)
}
