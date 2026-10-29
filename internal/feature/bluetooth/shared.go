package bluetooth

import (
	"sync"

	sharedproxy "github.com/ygelfand/libcountertop/pkg/bluetooth/proxy"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/ble"
	"github.com/ygelfand/echolocal/internal/layout"
)

type Proxy struct{ *sharedproxy.Proxy }

func init() { component.Register(sharedcomponent.Device, Get, sharedcomponent.Order(40)) }

var get = sync.OnceValue(func() *Proxy {
	p := &Proxy{sharedproxy.New(sharedproxy.Dependencies{
		Radio:     ble.Get(),
		Settings:  config.BluetoothSection,
		Reconnect: func() { component.Reconnect.Emit(struct{}{}) },
		Beacon: func() []byte {
			mac, _ := layout.FactoryMAC()
			return sharedproxy.BeaconAdvertisement(sharedproxy.BeaconMinor(mac))
		},
		Links: ble.Get(),
		Bonds: layout.BondsPath,
	})}
	component.Settings.Add(p.Controls())
	return p
})

func Get() *Proxy                      { return get() }
func (p *Proxy) Restore(config.Config) { p.Proxy.Restore() }
