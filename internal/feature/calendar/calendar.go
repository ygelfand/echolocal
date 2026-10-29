//go:build !board_biscuit

package calendar

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/homeassistant/calendar"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/product"
)

func init() {
	component.Register(sharedcomponent.Device, Get, sharedcomponent.Order(61))
}

type Calendar struct{ *calendar.Calendar }

func (c Calendar) Restore(cfg config.Config) { c.Publish(cfg.Calendar) }

var get = sync.OnceValue(func() *Calendar {
	return &Calendar{calendar.Build(product.Get(), config.CalendarSection)}
})

func Get() *Calendar { return get() }
