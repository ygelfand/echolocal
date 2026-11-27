//go:build !board_biscuit

package recipes

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/media/mealie"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/product"
	"github.com/ygelfand/echolocal/internal/feature/timer"
)

type componentKitchen struct{ *mealie.Kitchen }

func (k componentKitchen) Restore(c config.Config) { k.Kitchen.Restore(c.Mealie) }

var get = sync.OnceValue(func() *mealie.Kitchen {
	return mealie.Build(product.Get(), config.MealieSection, timer.Get())
})

func Get() *mealie.Kitchen { return get() }

func init() {
	component.Register(
		sharedcomponent.Network,
		func() componentKitchen { return componentKitchen{Get()} },
		component.Needs(board.Panel),
	)
}
