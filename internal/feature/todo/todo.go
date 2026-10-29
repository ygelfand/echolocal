//go:build !board_biscuit

package todo

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/homeassistant/todo"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/product"
)

func init() {
	component.Register(sharedcomponent.Device, Get, sharedcomponent.Order(61))
}

type Todo struct{ *todo.Todo }

func (t Todo) Restore(cfg config.Config) { t.Publish(cfg.Todo) }

var get = sync.OnceValue(
	func() *Todo { return &Todo{todo.Build(product.Get(), config.TodoSection)} },
)

func Get() *Todo { return get() }
