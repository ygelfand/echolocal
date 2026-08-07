// Package api presents the Dot to Home Assistant over the ESPHome native API.
//
// It owns no entities. What it serves is whatever the components registered, collected at start-up:
// their entities, and the handlers that answer without one.
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/android/firewall"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/bluetooth"
	"github.com/ygelfand/echolocal/internal/feature/voice"
	"github.com/ygelfand/echolocal/internal/feature/wakeword"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/safe"
)

func init() {
	// Last: it serves the registry, so nothing should still be coming up when it starts listening.
	component.Register(component.Network, Get(), component.Order(99))
}

type API struct {
	srv  *esphome.Server
	name string

	reconnect chan struct{}
	announced sync.Once
}

var (
	once   sync.Once
	shared *API
)

func Get() *API {
	once.Do(func() {
		shared = &API{reconnect: make(chan struct{}, 1)}
		component.Reconnect.Listen(func(struct{}) { shared.Reconnect() })
		component.Fire.Listen(func(e component.Event) { shared.fire(e) })
	})
	return shared
}

func (a *API) Name() string { return "api" }

// Start builds the server. Not the constructor, because what the server serves is the registry, and
// the registry is only complete once every package's init has run.
func (a *API) Start(context.Context) error {
	psk, err := loadPSK(layout.KeyPath)
	if err != nil {
		return err
	}
	mac, err := macAddress()
	if err != nil {
		return err
	}

	device := config.Get().Device
	ents := esphome.NewEntities()
	if err := ents.Add(component.Default().Entities()...); err != nil {
		return err
	}
	if err := ents.AddActions(component.Default().Actions()...); err != nil {
		return err
	}

	a.name = layout.Slug(device.Name)
	a.srv = &esphome.Server{
		Addr: device.Addr,
		Info: esphome.Info{
			Name:              a.name,
			FriendlyName:      device.Name,
			MACAddress:        mac,
			Manufacturer:      layout.Manufacturer,
			Model:             layout.Model,
			Version:           layout.Version,
			VoiceFeatures:     voice.Features,
			BluetoothFeatures: bluetooth.Get().Features(),

			Devices: subDevices(device.Name),
		},
		PSK:    psk,
		Logger: slog.Default(),
		// Persist a key Home Assistant pushes, or the next connection reverts to the old one.
		OnSetEncryptionKey: func(k esphome.PSK) error { return writePSK(layout.KeyPath, k) },

		OnSubscribed: func() { component.Subscribed.Emit(struct{}{}) },

		// The handlers components answer for themselves rather than through an entity: the voice
		// satellite's pipeline traffic, and the Bluetooth proxy's subscribe and set-mode messages.
		Handler: esphome.Chain(append([]esphome.Handler{ents}, component.Default().Handlers()...)...),
	}
	return nil
}

// subDevices groups entities onto pages of their own, since Home Assistant puts every entity a device has
// on one page and there are more here than anyone wants to read.
//
// Each name carries the device's own, because Home Assistant shows what it is given verbatim: a bare "Ring"
// or "Assistant 1" is unreadable in a house with several satellites.
func subDevices(name string) []esphome.Device {
	out := []esphome.Device{
		{ID: component.DeviceRing, Name: name + " ring"},
		{ID: component.DeviceMicrophone, Name: name + " microphone"},
		{ID: component.DevicePlayback, Name: name + " playback"},
	}

	for slot := range wakeword.Slots {
		out = append(out, esphome.Device{
			ID:   component.AssistantDevice(slot),
			Name: fmt.Sprintf("%s assistant %d", name, slot+1),
		})
	}
	return out
}

// Run listens until ctx is cancelled, advertising over mDNS so Home Assistant finds the device
// without being told an address.
func (a *API) Run(ctx context.Context) error {
	safe.Go("logs", func() { a.pipeLogs(ctx) })

	// A second way in to the port the install's hook already opens. It costs nothing, and it means a
	// hook that is missing, or older than the device, is not a lockout.
	if err := firewall.Open(firewall.API, layout.Port); err != nil {
		slog.Error("opening the api port failed", "port", layout.Port, "err", err)
	}

	for {
		ln, err := net.Listen("tcp", a.srv.Addr)
		if err != nil {
			return fmt.Errorf("api: listen %s: %w", a.srv.Addr, err)
		}

		a.announced.Do(func() {
			safe.Go("mdns", func() { a.advertise(ctx, ln.Addr().(*net.TCPAddr).Port) })
		})

		serving, stop := context.WithCancel(ctx)
		go func() {
			select {
			case <-a.reconnect:
			case <-serving.Done():
			}
			stop()
		}()

		err = a.srv.Serve(serving, ln)
		stop()

		if err != nil || ctx.Err() != nil {
			return err
		}

		// Between serving and listening again nothing reads Info, which is the only moment it can be
		// changed: a client is told what the device is once, when it connects.
		a.srv.Info.BluetoothFeatures = bluetooth.Get().Features()
		slog.Info("serving again", "bluetooth", a.srv.Info.BluetoothFeatures)
	}
}

// fire puts an event on Home Assistant's bus. Nothing happens before the server is up or while no
// client is subscribed: an event nobody is listening for is not a failure, and the component that
// asked for it has nothing useful to do about one.
func (a *API) fire(e component.Event) {
	if a.srv == nil {
		return
	}
	if err := a.srv.FireEvent(e.Name, e.Data); err != nil {
		slog.Debug("firing an event failed", "event", e.Name, "err", err)
	}
}

// Reconnect drops every client and serves afresh, which is how a change to what the device says it is
// reaches Home Assistant.
func (a *API) Reconnect() {
	select {
	case a.reconnect <- struct{}{}:
	default:
	}
}

// loadPSK reads the key echoctl wrote at install. With no key the device runs unprovisioned —
// Noise with the reserved zero key — so Home Assistant can push a real one, which is what
// `echoctl install --zero-psk` leaves behind. echod never invents a key: one that appeared on
// first boot would be unknown to Home Assistant and nobody would be told it changed.
func loadPSK(path string) (*esphome.PSK, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return esphome.Unprovisioned(), nil
	}
	k, err := esphome.ParsePSK(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("api: key at %s: %w", path, err)
	}
	return &k, nil
}

func writePSK(path string, k esphome.PSK) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(k.String()+"\n"), 0o600)
}

// macAddress is how Home Assistant recognises the device, and it refuses one whose address is not
// the address it registered. So there is no stand-in: without an address there is nothing to
// announce, and announcing the wrong one costs the pairing.
func macAddress() (string, error) {
	b, err := os.ReadFile(layout.MACPath)
	if err != nil {
		return "", fmt.Errorf("reading the device address: %w", err)
	}
	mac := layout.MAC(string(b))
	if mac == "" {
		return "", fmt.Errorf("%s holds %q, which is not an address", layout.MACPath, strings.TrimSpace(string(b)))
	}
	return mac, nil
}
