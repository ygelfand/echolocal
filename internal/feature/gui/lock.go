//go:build !board_biscuit

package gui

import (
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"

	"github.com/ygelfand/echolocal/internal/feature/lock"
)

func extendLock(look *widgets.UI, o *screens.Options, _ func() *screens.App) {
	o.Views = append(o.Views, lock.Get().Gate().Views(look))
}
