//go:build !board_biscuit

package radar

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/radar"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/product"
)

var get = sync.OnceValue(
	func() *radar.Radar { return radar.Build(product.Get(), config.WeatherSection) },
)

func Get() *radar.Radar { return get() }
