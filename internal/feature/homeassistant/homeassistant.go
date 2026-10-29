package homeassistant

import (
	"sync"

	sharedha "github.com/ygelfand/libcountertop/pkg/homeassistant"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/states"
)

var get = sync.OnceValue(func() *sharedha.Controller {
	c := sharedha.New(sharedha.Settings{Home: config.HomeSection, API: config.APISection})
	c.FollowIntegration(states.Get())
	return c
})

func Get() *sharedha.Controller { return get() }

func init() { component.Register(sharedcomponent.Device, Get, sharedcomponent.Order(5)) }
