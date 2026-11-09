package actions

import (
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	shared "github.com/ygelfand/libcountertop/pkg/actions"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/voice"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/alarm"
	"github.com/ygelfand/echolocal/internal/feature/broadcast"
	"github.com/ygelfand/echolocal/internal/feature/call"
	"github.com/ygelfand/echolocal/internal/feature/discovery"
)

func init() { component.Register(sharedcomponent.Network, Get) }

type Actions struct{ reg shared.Registry }

var get = sync.OnceValue(func() *Actions {
	a := &Actions{}
	a.reg.Add(call.Get().Action(discovery.Get().Peers, component.Board().CameraWidth > 0))
	a.reg.Add(broadcast.Get().Action())
	a.reg.Add(alarm.Get().VoiceAction())
	a.addTV()
	for _, action := range call.Get().ControlActions() {
		a.reg.Add(action)
	}
	component.Subscribed.Listen(func(struct{}) { a.Declare() })
	a.followIntegration()
	return a
})

func Get() *Actions { return get() }

func (a *Actions) Names() []string { return a.reg.Names() }
func (a *Actions) Declare() {
	voiced := config.Get().Voice.Action
	component.Fire.Emit(
		sharedcomponent.Event{
			Name: shared.Event,
			Data: a.reg.Declaration(voiced, voice.ActionSwitch),
		},
	)
}

func (a *Actions) Name() string { return "actions" }

func (a *Actions) Actions() []*esphome.Action { return a.reg.Actions() }
