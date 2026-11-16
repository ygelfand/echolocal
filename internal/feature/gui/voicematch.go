//go:build !board_biscuit

package gui

import (
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"

	"github.com/ygelfand/echolocal/internal/feature/voicematch"
	"github.com/ygelfand/echolocal/internal/feature/voicematch/panel"
)

func extendVoiceMatch(_ *widgets.UI, o *screens.Options, _ func() *screens.App) {
	panel.Follow()
	o.Views = append(o.Views, panel.Get().Screen)
	o.Redraws = append(o.Redraws, screens.On(&voicematch.Get().Changed))
	o.Badge = panel.People().Badge
}
