//go:build !board_biscuit

package actions

import (
	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/feature/recipes"
)

func (a *Actions) addRecipes() {
	if component.Board().Has(board.Panel) {
		a.reg.Add(recipes.Get().VoiceAction())
	}
}
