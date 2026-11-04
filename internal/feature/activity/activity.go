package activity

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/assistant/activity"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
)

func init() { component.Register(sharedcomponent.Device, Get) }

const (
	TurnEvent = "esphome.echolocal_turn"
	DuckEvent = "esphome.echolocal_duck"
)

var get = sync.OnceValue(func() *activity.Log {
	return activity.New(activity.Options{
		Emit: func(fields map[string]string) {
			component.Fire.Emit(sharedcomponent.Event{Name: TurnEvent, Data: fields})
		},
		Duck: func(fields map[string]string) {
			component.Fire.Emit(sharedcomponent.Event{Name: DuckEvent, Data: fields})
		},
		DuckDB: func() float64 { return config.Get().Media.DuckDB },
	})
})

func Get() *activity.Log { return get() }
