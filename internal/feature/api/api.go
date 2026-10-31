package api

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/homeassistant/nativeapi"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/runtime/service"

	"github.com/ygelfand/echolocal/internal/android/firewall"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/bluetooth"
	"github.com/ygelfand/echolocal/internal/feature/voice"
	"github.com/ygelfand/echolocal/internal/feature/wakeword"
	"github.com/ygelfand/echolocal/internal/feature/web"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/logging"
)

func init() {
	component.Register(sharedcomponent.Network, Get, sharedcomponent.Order(99),
		sharedcomponent.Supervise(service.Restart(2*time.Second, time.Minute)))
}

type API struct{ *nativeapi.Server }

var get = sync.OnceValue(func() *API {
	s := nativeapi.New(nativeapi.Dependencies{
		KeyPath:      layout.KeyPath,
		DialPath:     layout.DialPath,
		Identity:     identity,
		Bluetooth:    func() esphome.BluetoothFeature { return bluetooth.Get().Features() },
		Registry:     component.Default(),
		Adoption:     adoption{},
		Logs:         logging.Log.Stream,
		OnSubscribed: func() { component.Subscribed.Emit(struct{}{}) },
	})
	component.Reconnect.Listen(func(struct{}) { s.Reconnect() })
	component.Fire.Listen(s.Fire)
	component.Settings.Add(s.Controls())
	return &API{s}
})

func Get() *API { return get() }

func (a *API) Run(ctx context.Context) error {
	if err := firewall.Open(firewall.API, layout.Port); err != nil {
		slog.Error("opening the api port failed", "port", layout.Port, "err", err)
	}
	return a.Server.Run(ctx)
}

func Key() string { return nativeapi.ReadKey(layout.KeyPath) }

func identity() (nativeapi.Identity, error) {
	mac, err := layout.FactoryMAC()
	if err != nil {
		return nativeapi.Identity{}, err
	}

	device := config.Get().Device
	return nativeapi.Identity{
		Addr:     device.Addr,
		Platform: layout.Platform,
		Board:    device.Board.Codename,
		Info: esphome.Info{
			Name:          layout.Slug(device.Name),
			FriendlyName:  device.Name,
			MACAddress:    mac,
			Manufacturer:  layout.Manufacturer,
			Model:         device.Board.Model,
			Version:       layout.Version,
			ProjectName:   layout.Manufacturer + "." + device.Board.Codename,
			Devices:       devices(device.Name),
			VoiceFeatures: voice.Features,
			WebserverPort: web.Port,
		},
	}, nil
}

func devices(name string) []esphome.Device {
	all := []esphome.Device{
		{ID: component.DeviceRing, Name: name + " ring"},
		{ID: component.DeviceMicrophone, Name: name + " microphone"},
		{ID: component.DevicePlayback, Name: name + " playback"},
		{ID: component.DeviceScreen, Name: name + " screen"},
		{ID: component.DeviceCamera, Name: name + " camera"},
		{ID: component.DeviceDashboard, Name: name + " dashboard"},
		{ID: component.DeviceVoice, Name: name + " voice triggers"},
		{ID: component.DeviceFrontend, Name: name + " frontend"},
	}
	for slot := range wakeword.Slots {
		all = append(all, esphome.Device{
			ID:   component.AssistantDevice(slot),
			Name: fmt.Sprintf("%s assistant %d", name, slot+1),
		})
	}
	return all
}

type adoption struct{}

func (adoption) Adopted() bool            { return config.Get().API.Adopted }
func (adoption) SetAdopted(on bool) error { return config.Set().API().Adopted(on) }
