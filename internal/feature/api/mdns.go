package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/ygelfand/go-esphome-device/mdns"

	"github.com/ygelfand/echolocal/internal/hardware/metrics"
	"github.com/ygelfand/echolocal/internal/layout"
)

// mdnsRetry is how long to wait between attempts. There is no attempt limit: wifi can come back long
// after boot, and an advert that never reappears is a device that has to be found by address.
const mdnsRetry = 3 * time.Second

// advertise publishes the addresses the device actually has, and republishes them when they change.
//
// echod starts from init, well before wifi has associated. The addresses are passed rather than left
// to the library, which falls back to loopback when nothing routable exists — a record Home Assistant
// discovers and then cannot connect to, which is worse than no record at all. Discovery is a
// convenience and a device reachable by address works without it, so this only logs.
func (a *API) advertise(ctx context.Context, port int) {
	for attempt := 1; ; attempt++ {
		ips := metrics.Addresses()
		if len(ips) == 0 {
			if attempt == 1 {
				slog.Info("waiting for an address before advertising over mdns")
			}
			if !pause(ctx, mdnsRetry) {
				return
			}
			continue
		}

		adv, err := mdns.Advertise(mdns.Config{
			Name:         a.name,
			FriendlyName: a.srv.Info.FriendlyName,
			Port:         port,
			MACAddress:   a.srv.Info.MACAddress,
			Version:      a.srv.Info.Version,
			Platform:     layout.Platform,
			Board:        layout.Board,
			Encrypted:    a.srv.PSK != nil,
			IPs:          ips,
		})
		if err != nil {
			// Only the first failure is worth a warning: after that it is the expected state of a
			// device waiting for its network, and saying so every few seconds buries everything else.
			if attempt == 1 {
				slog.Warn("mdns advertise failed, retrying", "err", err)
			} else {
				slog.Debug("mdns advertise failed", "attempt", attempt, "err", err)
			}
			if !pause(ctx, mdnsRetry) {
				return
			}
			continue
		}

		slog.Info("advertising over mdns", "name", a.name, "port", port, "addrs", metrics.AddressKey(ips))

		// The registration stands until the addresses it was made with are no longer the ones the
		// device has: a lease that changed, or a network that arrived late.
		for metrics.AddressKey(metrics.Addresses()) == metrics.AddressKey(ips) {
			if !pause(ctx, mdnsRetry) {
				adv.Close()
				return
			}
		}
		adv.Close()
		slog.Info("addresses changed, re-advertising over mdns", "was", metrics.AddressKey(ips))
	}
}

// pause sleeps unless ctx ends first, reporting whether there is any point carrying on.
func pause(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
