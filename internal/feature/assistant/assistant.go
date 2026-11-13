package assistant

import (
	"sync"

	sharedscreen "github.com/ygelfand/libcountertop/pkg/display/assistant"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/idle"
	"github.com/ygelfand/echolocal/internal/feature/visuals"
	"github.com/ygelfand/echolocal/internal/feature/voice"
	"github.com/ygelfand/echolocal/internal/hardware/display"
	"github.com/ygelfand/echolocal/internal/hardware/gpu"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
)

func init() {
	component.Register(
		sharedcomponent.Device,
		Get,
		sharedcomponent.Order(45),
		component.Needs(board.Panel),
	)
}

var get = sync.OnceValue(func() *sharedscreen.Assistant {
	a := sharedscreen.New(sharedscreen.Dependencies{
		Microphone: mic.Get(),
		Controller: voice.Get(),
		Words:      config.WakeSection,
		Screen:     idle.Get(),
	})
	voice.Shown.Listen(a.Show)
	return a
})

func Get() *sharedscreen.Assistant { return get() }

var Stage = sync.OnceValue(func() *sharedscreen.Stage {
	return sharedscreen.NewStage(sharedscreen.StageDependencies{
		Screen:  Get(),
		GPU:     gpu.Get(),
		Display: display.Get(),
		Visuals: visuals.Get(),
		Wake:    config.WakeSection,
	})
})
