//go:build !board_biscuit

package thermostat

import (
	"sync"
	"sync/atomic"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/display/thermostatview"
	"github.com/ygelfand/libcountertop/pkg/display/visual"
	"github.com/ygelfand/libcountertop/pkg/display/visualview"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	shared "github.com/ygelfand/libcountertop/pkg/homeassistant/thermostat"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/dashboard"
	"github.com/ygelfand/echolocal/internal/feature/product"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/feature/visuals"
	"github.com/ygelfand/echolocal/internal/feature/weather"
	"github.com/ygelfand/echolocal/internal/hardware/display"
	"github.com/ygelfand/echolocal/internal/hardware/gpu"
)

func init() {
	component.Register(
		sharedcomponent.Network,
		func() *Thermostats { return &Thermostats{Get()} },
		component.Needs(board.Panel),
	)
}

var Get = sync.OnceValue(func() *shared.Thermostats {
	return shared.Build(product.Get(), config.ThermostatSection, func() string {
		r, _ := weather.Get().Now()
		return r.Unit
	})
})

var shown atomic.Pointer[thermostatview.View]

func Shown() *thermostatview.View { return shown.Load() }

func NewView(ui *widgets.UI) *thermostatview.View {
	v := thermostatview.New(thermostatview.Options{
		UI:          ui,
		Shell:       shell.Get(),
		Thermostats: Get(),
		Settings:    config.ThermostatSection,
		Clock24:     func() bool { return config.Get().Screen.Hours == schema.TwentyFourHour },
		Outside: func() (float64, bool) {
			r, ok := weather.Get().Now()
			if !ok || r.Temperature == nil {
				return 0, false
			}
			return *r.Temperature, true
		},
		Backdrop: func(kind visual.Kind, showing func() bool) thermostatview.Backdrop {
			return visualview.New(
				visualview.Fixed(visuals.Get(), kind),
				visualview.Dependencies{
					GPU:      gpu.Get(),
					Display:  display.Get(),
					Shell:    shell.Get(),
					Settings: config.VisualSection,
					Showing:  showing,
				},
			)
		},
		Tabbed: func() bool {
			t, tabbed := dashboard.Tabs().Showing()
			return tabbed && t.Kind == thermostatview.TabKind && !shell.Get().Covered()
		},
	})
	shown.Store(v)
	dashboard.Tabs().Add(45, v.DashboardTabs)
	return v
}

type Thermostats struct{ *shared.Thermostats }

func (t *Thermostats) Entities() []esphome.Entity { return t.Thermostats.Entities() }

func (t *Thermostats) Restore(cfg config.Config) { t.Thermostats.Restore(cfg.Thermostat) }
