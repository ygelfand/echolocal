package states

import (
	sharedstates "github.com/ygelfand/libcountertop/pkg/homeassistant/states"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/component"
)

type States = sharedstates.States
type Value = sharedstates.Value

var shared = sharedstates.New()

func Get() *States { return shared }
func init()        { component.Register(sharedcomponent.Device, Get, sharedcomponent.Order(5)) }
