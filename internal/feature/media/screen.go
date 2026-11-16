//go:build !board_biscuit

package media

import (
	"sync"

	sharedcard "github.com/ygelfand/libcountertop/pkg/display/mediacard"
	sharedshell "github.com/ygelfand/libcountertop/pkg/display/shell"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/apps"
	"github.com/ygelfand/echolocal/internal/feature/shell"
)

var cardOnce sync.Once
var theCard *sharedcard.Card

func card() *sharedcard.Card {
	cardOnce.Do(func() {
		theCard = sharedcard.New(sharedcard.Dependencies{
			Player: Get(),
			Idle:   config.IdleSection,
			Media:  config.MediaSection,
			Device: component.DevicePlayback,
			Shell:  shell.Get(),
		})
		component.Settings.Add(theCard.Settings())
	})
	return theCard
}
func Card() *sharedcard.Card { return card() }
func Page() sharedshell.View { return card().Page() }
func Showing() bool          { return shell.Get().Top() == Page() }
func onRail()                { apps.Get().Add(card().App()) }

func init() {
	component.Register(
		sharedcomponent.Device,
		func() *sharedcard.Card { c := card(); onRail(); Get().Begun.Listen(c.Began); return c },
		sharedcomponent.Order(39),
		component.Needs(board.Panel),
	)
}
func (p *Player) Open() { card().Open() }
