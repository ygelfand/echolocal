//go:build !board_biscuit

package gui

import (
	"sync"
	"time"

	"github.com/ygelfand/echolocal/internal/hardware/gpu"

	sharedvolume "github.com/ygelfand/libcountertop/pkg/audio/volume"
	"github.com/ygelfand/libcountertop/pkg/display/screens"
	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/runtime/service"
	tesseraview "github.com/ygelfand/libcountertop/pkg/tessera/widgets"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/assistant"
	"github.com/ygelfand/echolocal/internal/feature/cameras"
	"github.com/ygelfand/echolocal/internal/feature/dashboard"
	"github.com/ygelfand/echolocal/internal/feature/doorbell"
	"github.com/ygelfand/echolocal/internal/feature/drawer"
	"github.com/ygelfand/echolocal/internal/feature/firmware"
	"github.com/ygelfand/echolocal/internal/feature/frontend"
	"github.com/ygelfand/echolocal/internal/feature/homeassistant"
	"github.com/ygelfand/echolocal/internal/feature/homecontrol"
	"github.com/ygelfand/echolocal/internal/feature/idle"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/message"
	"github.com/ygelfand/echolocal/internal/feature/nws"
	"github.com/ygelfand/echolocal/internal/feature/poster"
	"github.com/ygelfand/echolocal/internal/feature/privacy"
	"github.com/ygelfand/echolocal/internal/feature/screen"
	"github.com/ygelfand/echolocal/internal/feature/settings"
	featureshell "github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/feature/tessera"
	"github.com/ygelfand/echolocal/internal/feature/timer"
	"github.com/ygelfand/echolocal/internal/feature/volume"
	"github.com/ygelfand/echolocal/internal/feature/weather"
	"github.com/ygelfand/echolocal/internal/feature/web"
	"github.com/ygelfand/echolocal/internal/hardware/display"
	hwtouch "github.com/ygelfand/echolocal/internal/hardware/touch"
	"github.com/ygelfand/echolocal/internal/lib/wake"
	echoui "github.com/ygelfand/echolocal/internal/ui"
	"github.com/ygelfand/echolocal/internal/ui/theme"
)

func init() {
	component.Register(
		sharedcomponent.Device,
		Get,
		sharedcomponent.Order(36),
		sharedcomponent.Supervise(service.Restart(time.Second, time.Minute)),
		component.Needs(board.Panel),
	)
}

var Get = sync.OnceValue(func() *screens.App {
	look := widgets.NewUI(config.ScreenSection)
	o := screens.Options{
		Name:       "echolocal",
		UISize:     component.Board().UISize,
		DeviceName: func() string { return config.Get().Device.Name },
		Settings: func() screens.Settings {
			c := config.Get()
			return screens.Settings{
				Screen:  c.Screen,
				Clock:   c.Clock,
				Idle:    c.Idle,
				Weather: c.Weather,
			}
		},
		UI:           look,
		Weather:      config.WeatherSection,
		Brand:        theme.Brand,
		Logo:         echoui.Logo().Light,
		Night:        echoui.Logo().Dark,
		Offering:     web.Get().Offering,
		Input:        frontend.Get().Input(),
		KeyboardPair: frontend.Get().KeyboardPair,

		Display:   display.Get(),
		GPU:       gpu.Get(),
		Touch:     hwtouch.Get(),
		Shell:     featureshell.Get(),
		Screen:    screen.Get().Screen,
		Messages:  message.Get(),
		Assistant: assistant.Get(),
		Stage:     assistant.Stage(),
		Timers:    timer.Get(),
		Poster:    poster.Get(),
		Drawer:    drawer.Get(),
		Tabs:      dashboard.Tabs(),
		Home:      homecontrol.Get(),
		HomeHA:    homeassistant.Get(),
		Saver:     idle.Get(),
		Visuals:   settings.Visuals(),
		Tessera: tesseraview.New(tesseraview.Dependencies{
			UI:      look,
			Screens: tessera.Get(),
			Perform: homeassistant.Get().Perform,

			HomeAfterIdle: homecontrol.Get().ReturnHome,
			Logo:          echoui.Logo(),
		}),
		Firmware: tessera.Firmware(),
		Forecast: weather.Get(),
		Warnings: nws.Get(),

		WeatherEntities: func() shell.View { return settings.WeatherEntities() },
		CamerasPage:     func() shell.View { return cameras.Get().Page() },
		DoorbellPage:    func() shell.View { return doorbell.View().Page() },
		Player:          media.Get(),
		PlayerPage:      media.Page,
		MediaVolume:     sharedvolume.For(volume.Get(), config.StreamMedia),
		Volume:          volume.Get(),
		Streams:         config.Streams(),

		Upgrade: func() widgets.Upgrade {
			if up := firmware.Get().Upgrade(); up.Active() {
				return widgets.Upgrade{Active: true, At: up.At, Version: up.Version}
			}
			p := wake.Lib().Preparation()
			return widgets.Upgrade{Active: p.Active(), At: p.At, Label: p.Phrase}
		},
		Upgrading: screens.All(
			screens.On(&firmware.Get().Upgrading),
			screens.On(&wake.Lib().Preparing),
		),
		Marks: func() screens.Marks {
			m := privacy.Get().Current()
			return screens.Marks{Mic: m.MicMuted, Camera: m.CameraBlocked}
		},
		Redraws: []screens.Watch{
			screens.On(&firmware.Get().Offered),
			screens.On(&firmware.Get().Listed),
			screens.On(&privacy.Get().Changed),
			screens.On(&web.Get().Offered),
		},
	}
	var app *screens.App
	self := func() *screens.App { return app }
	for _, extend := range []func(*widgets.UI, *screens.Options, func() *screens.App){
		extendAlarms,
		extendApps,
		extendCalendar,
		extendCalls,
		extendCameras,
		extendLock,
		extendMirror,
		extendRadar,
		extendRadio,
		extendShopping,
		extendSync,
	} {
		extend(look, &o, self)
	}
	app = screens.New(o)
	return app
})
