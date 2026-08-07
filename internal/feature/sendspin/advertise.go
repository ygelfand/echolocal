package sendspin

import (
	"context"
	"log/slog"
	"time"

	"github.com/libp2p/zeroconf/v2"

	"github.com/ygelfand/echolocal/internal/hardware/metrics"
)

// What a server browses for. The path is required by the spec — it is how a server knows where to
// point its WebSocket once mDNS has told it the address.
const (
	service = "_sendspin._tcp"
	domain  = "local."
	path    = "/sendspin"
)

// retry is how long to wait between attempts. There is no limit: wifi can come back long after boot,
// and an advert that never reappears is a room nothing can find.
const retry = 3 * time.Second

// advertise publishes the room and republishes it when the addresses change. Addresses are passed
// because the library falls back to loopback without them.
func advertise(ctx context.Context, name string, port int) {
	for attempt := 1; ; attempt++ {
		ips := metrics.Addresses()
		if len(ips) == 0 {
			if attempt == 1 {
				slog.Info("waiting for an address before advertising sendspin")
			}
			if !pause(ctx, retry) {
				return
			}
			continue
		}

		addrs := make([]string, 0, len(ips))
		for _, ip := range ips {
			addrs = append(addrs, ip.String())
		}

		srv, err := zeroconf.RegisterProxy(
			name, service, domain, port, name, addrs,
			[]string{"path=" + path, "name=" + name},
			nil,
		)
		if err != nil {
			// Only the first failure is worth a warning: after that it is the expected state of a
			// device waiting for its network, and saying so every few seconds buries everything else.
			if attempt == 1 {
				slog.Warn("sendspin advertise failed, retrying", "err", err)
			} else {
				slog.Debug("sendspin advertise failed", "attempt", attempt, "err", err)
			}
			if !pause(ctx, retry) {
				return
			}
			continue
		}

		slog.Info("advertising sendspin", "name", name, "port", port, "addrs", metrics.AddressKey(ips))

		// The registration stands until the addresses it was made with are no longer the ones the
		// device has: a lease that changed, or a network that arrived late.
		for metrics.AddressKey(metrics.Addresses()) == metrics.AddressKey(ips) {
			if !pause(ctx, retry) {
				srv.Shutdown()
				return
			}
		}
		srv.Shutdown()
		slog.Info("addresses changed, re-advertising sendspin", "was", metrics.AddressKey(ips))
	}
}

func pause(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
