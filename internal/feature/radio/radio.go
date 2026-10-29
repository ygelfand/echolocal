//go:build !board_biscuit

package radio

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/homeassistant/radio"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/product"
	"github.com/ygelfand/echolocal/internal/layout"
)

var get = sync.OnceValue(func() *radio.Radio {
	return radio.Build(product.Get(), config.RadioSection, func() string {
		mac, _ := layout.FactoryMAC()
		return mac
	})
})

func Get() *radio.Radio { return get() }
