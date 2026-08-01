package satellite

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/ygelfand/echolocal/internal/alog"
	"github.com/ygelfand/echolocal/internal/ble"
	"github.com/ygelfand/echolocal/internal/settings"
)

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
type bluetooth struct {
	proxy *esphome.BluetoothProxy
	radio *ble.Radio

	// reconnect asks the satellite to serve again, which is how a client learns the device now
	// advertises a proxy: device info is read once per connection.
	reconnect func()

	wanted  chan struct{}
	reports chan ble.Advertisement
	dropped atomic.Uint64

	mu     sync.Mutex
	active bool
}

func newBluetooth() *bluetooth {
	b := &bluetooth{
		proxy:   &esphome.BluetoothProxy{},
		radio:   ble.Get(),
		wanted:  make(chan struct{}, 1),
		reports: make(chan ble.Advertisement, queued),
	}

	b.proxy.OnSubscribed = func(bool) { b.apply() }
	b.proxy.OnMode = func(bool) { b.apply() }

	go alog.Safely("ble radio", b.settle)
	go alog.Safely("ble reports", b.deliver)
	return b
}

// Enabled reports whether the user has asked for the proxy.
func (b *bluetooth) Enabled() bool {
	return settings.Get().Bluetooth.ProxyOr(settings.DefaultBluetoothProxy)
}

// Features is what to advertise, which is nothing at all while the proxy is off.
func (b *bluetooth) Features() esphome.BluetoothFeature {
	if b.Enabled() {
		return bluetoothFeatures
	}
	return 0
}

// Enable follows the switch. The advertised features change on the reconnect that follows.
func (b *bluetooth) Enable(bool) {
	b.apply()
	if b.reconnect != nil {
		b.reconnect()
	}
}

// apply asks for the radio to be brought in line, without waiting for it.
func (b *bluetooth) apply() {
	select {
	case b.wanted <- struct{}{}:
	default:
	}
}

func (b *bluetooth) settle() {
	for range b.wanted {
		b.tune()
	}
}

// tune starts or stops the radio to match what is wanted, and reports which. A change of mode is a
// restart: the scan type is fixed when scanning begins.
func (b *bluetooth) tune() {
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
func (b *bluetooth) found(a ble.Advertisement) {
	select {
	case b.reports <- a:
	default:
		if n := b.dropped.Add(1); n%1000 == 1 {
			slog.Warn("ble reports dropped, home assistant is behind", "total", n)
		}
	}
}

// deliver batches what the radio heard and hands it over.
func (b *bluetooth) deliver() {
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
