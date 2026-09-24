// Package clock keeps the device's time right, and says where it got it.
//
// Amazon's sntp daemon did this with a server list compiled into it, tried an HTTP time source first
// and never wrote the hardware clock, so every boot began in 2010 until it found the network. This
// asks the servers the local network names first, falls back to public pools, and puts what it
// learns in the RTC so the next boot starts from it.
package clock

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/android/lease"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/hardware/metrics"
	"github.com/ygelfand/echolocal/internal/lib/sntp"
)

func init() {
	component.Register(component.Network, Get, component.Order(5))
}

// How often the clock is checked once it has been set, how each server is given to answer, and how
// long to wait after a round in which nobody did. A quartz oscillator drifts tens of milliseconds an
// hour at most, so an hour between checks keeps the slews small.
const (
	syncEvery    = time.Hour
	queryTimeout = 5 * time.Second
	retryMin     = 30 * time.Second
	retryMax     = 10 * time.Minute
	networkPoll  = 3 * time.Second

	// Below deadband the clock is left alone; up to maxSlew it is slewed, which nothing notices; past
	// that it is stepped, which is what a boot from a stale RTC needs.
	deadband = 10 * time.Millisecond

	// ConfigKey is where echod.yaml can name time servers, tried after the ones DHCP offered.
	ConfigKey = "ntp.servers"
)

// fallback is asked when neither the network nor the configuration names a server.
var fallback = []string{"pool.ntp.org", "time.cloudflare.com", "time.google.com"}

// source is a server and where the room heard of it, for the sensor.
type source struct {
	addr string
	via  string
}

// Clock is the component.
type Clock struct {
	state *esphome.TextSensor
	sync  *esphome.Button

	// leasePath and configured are how the tests point it away from the device.
	leasePath  string
	configured func() []string

	mu     sync.Mutex
	synced bool
	last   time.Time
	wake   chan struct{}
}

var (
	once   sync.Once
	shared *Clock
)

func Get() *Clock {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Clock {
	c := &Clock{
		leasePath:  lease.Path,
		configured: func() []string { return viper.GetStringSlice(ConfigKey) },
		wake:       make(chan struct{}, 1),
	}
	c.state = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "clock_source",
			Name:     "Clock source",
			Icon:     "mdi:clock-check-outline",
			Category: esphome.CategoryDiagnostic,
		},
	}
	c.state.Set("unsynced")

	c.sync = &esphome.Button{
		Base: esphome.Base{
			ObjectID: "clock_sync",
			Name:     "Sync clock",
			Icon:     "mdi:clock-fast",
			Category: esphome.CategoryConfig,
		},
		OnPress: c.rethink,
	}
	return c
}

func (c *Clock) Name() string { return "clock" }

func (c *Clock) Entities() []esphome.Entity { return []esphome.Entity{c.state, c.sync} }

// rethink asks for a sync now rather than at the next tick.
func (c *Clock) rethink() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Run keeps the clock right for as long as the process lives.
func (c *Clock) Run(ctx context.Context) error {
	// Nothing to ask until there is a network to ask over.
	for len(metrics.Addresses()) == 0 {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(networkPoll):
		}
	}

	retry := retryMin
	for {
		wait := syncEvery
		if err := c.once(ctx); err != nil {
			slog.Warn("clock sync failed", "err", err)
			wait, retry = retry, min(retry*2, retryMax)
		} else {
			retry = retryMin
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		case <-c.wake:
		}
	}
}

// once asks the servers in turn and applies the first answer.
func (c *Clock) once(ctx context.Context) error {
	sources := c.candidates()
	if len(sources) == 0 {
		return sntp.ErrNoServers
	}

	var last error
	for _, s := range sources {
		qctx, cancel := context.WithTimeout(ctx, queryTimeout)
		r, err := sntp.Query(qctx, s.addr)
		cancel()
		if err != nil {
			last = err
			slog.Debug("clock server did not answer", "server", s.addr, "via", s.via, "err", err)
			continue
		}
		return c.apply(r, s)
	}
	return fmt.Errorf("no time server answered: %w", last)
}

// candidates is who to ask, in order: what DHCP offered, what the configuration names, then the
// public pools. The lease is read every time because a device that moved network has a new one.
func (c *Clock) candidates() []source {
	var out []source
	seen := map[string]bool{}
	add := func(addr, via string) {
		addr = strings.TrimSpace(addr)
		if addr == "" || seen[addr] {
			return
		}
		seen[addr] = true
		out = append(out, source{addr: addr, via: via})
	}

	if ips, err := lease.NTPServers(c.leasePath); err == nil {
		for _, ip := range ips {
			add(ip.String(), "dhcp")
		}
	}
	for _, s := range c.configured() {
		add(s, "config")
	}
	for _, s := range fallback {
		add(s, "default")
	}
	return out
}

// apply moves the clock by what the server said, the gentle way when it can, and records the result.
func (c *Clock) apply(r sntp.Result, s source) error {
	how := "kept"
	off := r.Offset

	// right is what the clock should read. After a step the system clock already says so; after a
	// slew or nothing it is still the offset away, and the RTC is written with the corrected time
	// either way.
	right := func() time.Time { return time.Now().Add(off) }

	switch {
	case off > -deadband && off < deadband:
	case off > -maxSlew && off < maxSlew:
		if err := slew(off); err != nil {
			return err
		}
		how = "slewed"
	default:
		if err := step(right()); err != nil {
			return err
		}
		how = "stepped"
		right = time.Now
		slog.Warn("clock stepped", "by", off, "server", r.Server, "via", s.via)
	}

	// The RTC is written after every answer, not only a correction: it is what the next boot starts
	// from, and a boot that starts within a slew of right never has to step.
	if err := writeRTC(right()); err != nil {
		slog.Debug("clock rtc", "err", err)
	}

	c.mu.Lock()
	c.synced = true
	c.last = time.Now()
	c.mu.Unlock()

	c.state.Set(fmt.Sprintf("%s (%s), %s %s", host(r.Server), s.via, how, off.Round(time.Millisecond)))
	slog.Info("clock synced", "server", r.Server, "via", s.via, "offset", off.Round(time.Microsecond),
		"delay", r.Delay.Round(time.Microsecond), "stratum", r.Stratum, "how", how)
	return nil
}

// host is the server without its port, for the sensor.
func host(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// Synced reports whether the clock has been set since start-up, and when.
func (c *Clock) Synced() (bool, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.synced, c.last
}
