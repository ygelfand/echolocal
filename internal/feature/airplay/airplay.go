package airplay

import (
	"log/slog"
	"sync"

	sharedairplay "github.com/ygelfand/libcountertop/pkg/media/airplay"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/android/firewall"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/playout"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/parts"
)

func init() {
	component.Register(sharedcomponent.Device, Get, sharedcomponent.Order(61))
}

type Receiver struct{ *sharedairplay.Receiver }

func (r *Receiver) Restore(cfg config.Config) { r.Receiver.Restore(cfg.AirPlay) }

func (r *Receiver) Close() error {
	r.Stop()
	return nil
}

var get = sync.OnceValue(func() *Receiver {
	o := sharedairplay.Options{
		Settings: config.AirPlaySection,
		Device:   component.DevicePlayback,
		Helper:   layout.AirPlayHelper,
		Binary:   parts.AirPlay(),
		Keyfile:  layout.AirPlayKey,
		Name:     deviceName,
		MAC:      mac,
		Output: func(ended func()) playback.Output {
			o := playout.New()
			o.SetEnded(ended)
			return o
		},
		OpenPorts: func() {
			err := firewall.Allow(firewall.AirPlay, sharedairplay.TCPPorts, sharedairplay.UDPPorts)
			if err != nil {
				slog.Error("opening the airplay ports failed", "err", err)
			}
		},
		Latency:  speaker.Get().Latency,
		Rate:     speaker.Rate,
		Channels: speaker.Channels,
	}
	screen(&o)
	r := &Receiver{sharedairplay.NewReceiver(o)}
	component.Settings.Add(r.Settings())
	return r
})

func Get() *Receiver { return get() }

func deviceName() string {
	if name := config.Get().Device.Name; name != "" {
		return name
	}
	return component.Board().DefaultName
}

func mac() string {
	m, err := layout.FactoryMAC()
	if err != nil {
		slog.Warn("airplay has no hardware address", "err", err)
	}
	return m
}
