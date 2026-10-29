package product

import (
	"sync"

	sharedproduct "github.com/ygelfand/libcountertop/pkg/runtime/product"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/feature/homeassistant"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/feature/states"
	"github.com/ygelfand/echolocal/internal/layout"
)

var get = sync.OnceValue(func() sharedproduct.Context {
	return sharedproduct.Context{
		Name:       layout.Manufacturer,
		Version:    layout.Version,
		Site:       "github.com/ygelfand/echolocal",
		StateDir:   layout.StateDir,
		Controller: homeassistant.Get(),
		States:     states.Get(),
		Settings:   &component.Settings,
		Redraw:     func() { shell.Get().Redraw() },
		Screen:     component.DeviceScreen,
		Camera:     component.DeviceCamera,
	}
})

func Get() sharedproduct.Context { return get() }
