// Package bluetooth forwards what the BLE radio hears to Home Assistant, so a device out of range
// of everything else is in range of this one.
package bluetooth

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/ygelfand/echolocal/internal/alog"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/ble"
)

func init() {
	component.Register(component.Device, Get(), component.Order(40))
}

const (
	// batch is how many reports go in one message, and hold how long to wait for them.
	batch = 16
	hold  = 250 * time.Millisecond

	// queued is how many reports may wait for Home Assistant. Past this they are dropped: the radio
	// must never wait on the network, or the controller's own queue overflows and the driver starts
	// discarding packets for everything on the chip, wifi included.
	queued = 512
)

// bluetoothFeatures is what the device advertises when the proxy is on.
const bluetoothFeatures = esphome.BluetoothPassiveScan |
	esphome.BluetoothRawAdvertisements |
	esphome.BluetoothStateAndMode

// bluetooth carries what the radio hears to Home Assistant. The switch says whether the radio may
// run at all, and the subscription whether anyone is reading.
//
// Three goroutines, deliberately: the radio's reader only ever hands a report to a channel, a sender
// batches and writes them, and a third starts and stops the radio. Handlers run on the connection's
// goroutine, and HCI commands wait on the controller, so doing that work inline stops the connection
// answering anything at all.
type Proxy struct {
	proxy  *esphome.BluetoothProxy
	radio  *ble.Radio
	enable *esphome.Switch

	wanted  chan struct{}
	reports chan ble.Advertisement
	dropped atomic.Uint64

	mu     sync.Mutex
	active bool
}

var (
	once   sync.Once
	shared *Proxy
)

func Get() *Proxy {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Proxy {
	b := &Proxy{
		proxy:   &esphome.BluetoothProxy{},
		radio:   ble.Get(),
		wanted:  make(chan struct{}, 1),
		reports: make(chan ble.Advertisement, queued),
		enable: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "bluetooth_proxy",
				Name:     "Bluetooth proxy",
				Icon:     "mdi:bluetooth",
				Category: esphome.CategoryConfig,
			},
		},
	}

	b.proxy.OnSubscribed = func(bool) { b.apply() }
	b.proxy.OnMode = func(bool) { b.apply() }

	b.enable.Set(config.Get().Bluetooth.Proxy)
	b.enable.OnCommand = func(on bool) {
		b.enable.Set(on)
		if err := config.Set().Bluetooth().Proxy(on); err != nil {
			slog.Error("saving the bluetooth proxy setting failed", "err", err)
			return
		}
		b.apply()

		// What the device advertises has changed, and that is only read when a client connects.
		component.Reconnect.Emit(struct{}{})
	}

	go alog.Safely("ble radio", b.settle)
	go alog.Safely("ble reports", b.deliver)
	return b
}

func (b *Proxy) Name() string { return "bluetooth proxy" }

func (b *Proxy) Entities() []esphome.Entity { return []esphome.Entity{b.enable} }

// Handle answers the proxy's own protocol messages: subscribe, unsubscribe, set mode.
func (b *Proxy) Handle(ctx context.Context, conn *esphome.Conn, msg proto.Message) error {
	return b.proxy.Handle(ctx, conn, msg)
}

// Enabled reports whether the user has asked for the proxy.
func (b *Proxy) Enabled() bool { return config.Get().Bluetooth.Proxy }

// Reports is how many advertisements the radio has heard, for diagnostics.
func (b *Proxy) Reports() uint64 { return b.radio.Reports() }

// Features is what to advertise, which is nothing at all while the proxy is off.
func (b *Proxy) Features() esphome.BluetoothFeature {
	if b.Enabled() {
		return bluetoothFeatures
	}
	return 0
}

// apply asks for the radio to be brought in line, without waiting for it.
func (b *Proxy) apply() {
	select {
	case b.wanted <- struct{}{}:
	default:
	}
}

func (b *Proxy) settle() {
	for range b.wanted {
		b.tune()
	}
}

// tune starts or stops the radio to match what is wanted, and reports which. A change of mode is a
// restart: the scan type is fixed when scanning begins.
func (b *Proxy) tune() {
	want := b.Enabled() && b.proxy.Subscribed()
	active := b.proxy.Active()

	b.mu.Lock()
	settled := want == b.radio.Scanning() && (!want || active == b.active)
	b.active = active
	b.mu.Unlock()

	if settled {
		return
	}
	if b.radio.Scanning() {
		b.radio.Stop()
	}
	if !want {
		_ = b.proxy.Report(esphome.ScannerStopped)
		return
	}

	if err := b.radio.Start(active, b.found); err != nil {
		slog.Error("ble scan failed to start", "err", err)
		_ = b.proxy.Report(esphome.ScannerFailed)
		return
	}
	slog.Info("ble scanning", "active", active)
	_ = b.proxy.Report(esphome.ScannerRunning)
}

// found runs on the radio's reader. It must not block: anything slow here stalls the reader, and the
// controller's queue overflows into the kernel log.
func (b *Proxy) found(a ble.Advertisement) {
	select {
	case b.reports <- a:
	default:
		if n := b.dropped.Add(1); n%1000 == 1 {
			slog.Warn("ble reports dropped, home assistant is behind", "total", n)
		}
	}
}

// deliver batches what the radio heard and hands it over.
func (b *Proxy) deliver() {
	var pending []*api.BluetoothLERawAdvertisement

	flush := time.NewTimer(hold)
	defer flush.Stop()
	flush.Stop()

	send := func() {
		if len(pending) == 0 {
			return
		}
		ads := pending
		pending = nil

		if err := b.proxy.Advertise(ads); err != nil {
			slog.Debug("ble advertisements not delivered", "count", len(ads), "err", err)
		}
	}

	for {
		select {
		case a := <-b.reports:
			pending = append(pending, &api.BluetoothLERawAdvertisement{
				Address:     a.Addr(),
				Rssi:        int32(a.RSSI),
				AddressType: uint32(a.AddressType),
				Data:        a.Data,
			})
			if len(pending) == 1 {
				flush.Reset(hold)
			}
			if len(pending) >= batch {
				flush.Stop()
				send()
			}

		case <-flush.C:
			send()
		}
	}
}
