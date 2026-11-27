//go:build !board_biscuit

package gui

import (
	"github.com/ygelfand/libcountertop/pkg/display/recipes"
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"

	"github.com/ygelfand/echolocal/internal/feature/apps"
	"github.com/ygelfand/echolocal/internal/feature/commands"
	featurerecipes "github.com/ygelfand/echolocal/internal/feature/recipes"
	"github.com/ygelfand/echolocal/internal/feature/shell"
)

func extendRecipes(look *widgets.UI, o *screens.Options, _ func() *screens.App) {
	kitchen := recipes.New(recipes.Options{
		UI:      look,
		Shell:   shell.Get(),
		Kitchen: featurerecipes.Get(),
		Voice:   commands.Get(),
		Redraw:  func() { shell.Get().Redraw() },
	})
	apps.Get().Add(kitchen.App())
	o.Views = append(o.Views, kitchen.Screen)
}
