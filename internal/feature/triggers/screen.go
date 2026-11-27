//go:build !board_biscuit

package triggers

import (
	"github.com/ygelfand/libcountertop/pkg/media/mealie"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/voice"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/feature/commands"
	"github.com/ygelfand/echolocal/internal/feature/homeassistant"
)

func commandNames() []string {
	if !component.Board().Has(board.Panel) {
		return nil
	}
	out := []string{"radar", mealie.CookCommand}
	if component.Board().CameraWidth > 0 {
		out = append(out, "mirror")
	}
	return out
}

func applyCommands() { commands.Get().Apply() }

func labelChoices() func() []setting.Option { return voice.LabelChoices(homeassistant.Get()) }
