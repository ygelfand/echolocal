//go:build !board_biscuit

package homealarm

import (
	"sync"
	"sync/atomic"

	"github.com/ygelfand/echolocal/internal/feature/dashboard"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/display/homealarmview"
	sharedshell "github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/visual"
	"github.com/ygelfand/libcountertop/pkg/display/visualview"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	shared "github.com/ygelfand/libcountertop/pkg/homeassistant/homealarm"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/alerts"
	"github.com/ygelfand/echolocal/internal/feature/idle"
	"github.com/ygelfand/echolocal/internal/feature/product"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/feature/visuals"
	"github.com/ygelfand/echolocal/internal/hardware/display"
	"github.com/ygelfand/echolocal/internal/hardware/gpu"
)

func init() {
	component.Register(
		sharedcomponent.Network,
		func() *Alarm { return &Alarm{Get()} },
		component.Needs(board.Panel),
	)
}

var Get = sync.OnceValue(func() *shared.Alarm {
	return shared.Build(product.Get(), config.HomeAlarmSection, alerts.Get().Tones)
})

var shown atomic.Pointer[homealarmview.View]

func Shown() *homealarmview.View { return shown.Load() }

func NewView(ui *widgets.UI) *homealarmview.View {
	v := homealarmview.New(homealarmview.Options{
		UI:       ui,
		Shell:    shell.Get(),
		Alarm:    Get(),
		Settings: config.HomeAlarmSection,
		Ring:     func(c schema.Chime) { alerts.Get().Ring(c) },
		Chime:    func(c schema.Chime) { alerts.Get().Chime(c, 0) },
		Quiet:    func() { alerts.Get().Quiet() },
		Wake:     func() { idle.Get().Wake() },
		Clock24:  func() bool { return config.Get().Screen.Hours == schema.TwentyFourHour },
		Backdrop: func(owner sharedshell.View) homealarmview.Backdrop {
			return visualview.New(
				visualview.Fixed(visuals.Get(), visual.Matrix),
				visualview.Dependencies{
					GPU:      gpu.Get(),
					Display:  display.Get(),
					Shell:    shell.Get(),
					Settings: config.VisualSection,
					Owner:    owner,
				},
			)
		},
	})
	shown.Store(v)
	dashboard.Tabs().Add(40, v.DashboardTabs)
	return v
}

type Alarm struct{ *shared.Alarm }

func (a *Alarm) Entities() []esphome.Entity { return a.Alarm.Entities() }

func (a *Alarm) Restore(cfg config.Config) { a.Alarm.Restore(cfg.HomeAlarm) }
