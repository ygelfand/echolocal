package voice

import (
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/assistant/satellite"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/alarm"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/timer"
	"github.com/ygelfand/echolocal/internal/feature/wakeobserve"
	"github.com/ygelfand/echolocal/internal/feature/wakeword"
	"github.com/ygelfand/echolocal/internal/hardware/buttons"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/lib/wake"
)

func init() {
	component.Register(sharedcomponent.Device, Get, sharedcomponent.Order(30))
}

const Features = satellite.Features

type Voice struct{ *satellite.Satellite }

var (
	once   sync.Once
	shared *Voice
)

func Get() *Voice {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Voice {
	ours := wake.Lib().Ours()
	slots := wanted(ours, wakeword.Slots)

	v := &Voice{satellite.New(
		&esphome.VoiceSatellite{OnExternalWakeWords: wake.Lib().Answer},
		dependencies(),
		satellite.Options{
			OnTrace: wakeobserve.Get().Mark,
			Slots:   wakeword.Slots,
			Initial: slots,
			Timers:  timer.Get(),
			Alarms:  alarm.Get(),
			Sound:   speaker.Sound(),
			Player:  media.Get(),
			Save:    func(slot int, id string) error { return config.Set().Wake(slot).ID(id) },
		},
	)}
	slog.Info("wake words", "ours", len(ours), "slots", slots)

	wakeword.Requested.Listen(func(slot int) {
		wakeobserve.Get().Mark("wake_requested", slot)
		v.Start(slot)
	})

	buttons.Get().Events.Listen(func(e buttons.Event) {
		if e.Name != buttons.Action {
			return
		}
		switch e.Kind {
		case buttons.Tap:
			wakeobserve.Get().Mark("button_tap", 0)
			v.Action()
		case buttons.Hold:
			wakeobserve.Get().Mark("button_hold", 1)
			v.ActionHold()
		}
	})

	return v
}

func (v *Voice) ActionHold() { v.Satellite.ActionHold(1) }
