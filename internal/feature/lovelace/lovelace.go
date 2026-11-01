//go:build !board_biscuit

package lovelace

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/homeassistant/lovelaceview"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/chromecast"
	"github.com/ygelfand/echolocal/internal/feature/product"
	"github.com/ygelfand/echolocal/internal/layout"
)

func init() {
	component.Register(sharedcomponent.Network, Get, component.Needs(board.Panel))
}

var Get = sync.OnceValue(func() *lovelaceview.Feature {
	return lovelaceview.Build(
		product.Get(),
		config.LovelaceSection,
		chromecast.Engine(),
		func() string {
			return layout.Slug(config.Get().Device.Name)
		},
	)
})
