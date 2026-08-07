package sendspin

import (
	"context"
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/android/firewall"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/lib/safe"
)

func init() {
	component.Register(component.Device, Get(), component.Order(26))
}

// Player is the room's membership of a group, as Home Assistant sees it: a listening port and an
// advert saying the room is here.
type Player struct {
	enabled *esphome.Switch
	state   *esphome.TextSensor

	out *out

	mu      sync.Mutex
	running context.CancelFunc
	wake    chan struct{}
}

var (
	once   sync.Once
	shared *Player
)

func Get() *Player {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Player {
	p := &Player{
		out:  newOut(speaker.Get()),
		wake: make(chan struct{}, 1),
	}

	p.enabled = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "sendspin",
			Name:     "Sendspin",
			Icon:     "mdi:speaker-multiple",
			Category: esphome.CategoryConfig,
			DeviceID: component.DevicePlayback,
		},
		OnCommand: func(on bool) {
			p.enabled.Set(on)
			if err := config.Set().Sendspin().Enabled(on); err != nil {
				slog.Error("saving a setting failed", "setting", p.enabled.ObjectID, "err", err)
			}
			p.rethink()
		},
	}

	p.state = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "sendspin_state",
			Name:     "Sendspin state",
			Icon:     "mdi:lan-connect",
			Category: esphome.CategoryDiagnostic,
			DeviceID: component.DevicePlayback,
		},
	}
	p.state.Set(stateOff)
	return p
}

// What the state sensor says, from switched off to audible.
const (
	stateOff     = "off"
	stateWaiting = "waiting"
	stateJoined  = "joined"
	statePlaying = "playing"
)

func (p *Player) Name() string { return "sendspin" }

func (p *Player) Entities() []esphome.Entity {
	return []esphome.Entity{p.enabled, p.state}
}

// Restore puts the switch back where it was left. Listening waits for Run, once there is a network.
func (p *Player) Restore(c config.Config) { p.enabled.Set(c.Sendspin.Enabled) }

// Run holds the port open for as long as the switch is on.
func (p *Player) Run(ctx context.Context) error {
	defer p.stop()

	for {
		p.settle(ctx)

		select {
		case <-ctx.Done():
			return nil
		case <-p.wake:
		}
	}
}

// rethink wakes the loop without blocking. A second ask while one is pending is the same ask.
func (p *Player) rethink() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// settle makes what is running match what was asked for.
func (p *Player) settle(parent context.Context) {
	want := config.Get().Sendspin.Enabled

	p.mu.Lock()
	already := p.running != nil
	p.mu.Unlock()

	switch {
	case want && already, !want && !already:
		return
	case !want:
		p.stop()
		p.state.Set(stateOff)
		return
	}

	ctx, cancel := context.WithCancel(parent)
	p.mu.Lock()
	p.running = cancel
	p.mu.Unlock()

	// The vendor's chain drops what it was not told about.
	if err := firewall.Open(firewall.Sendspin, Port); err != nil {
		slog.Error("opening the sendspin port failed", "port", Port, "err", err)
	}

	name := config.Get().Device.Name
	l := newListener(p.out, speaker.Sound().Backgrounds(), p.state.Set)

	safe.Go("sendspin listen", func() {
		if err := l.serve(ctx, name); err != nil {
			slog.Error("sendspin listener stopped", "err", err)
		}
	})
	safe.Go("sendspin advertise", func() { advertise(ctx, name, Port) })

	p.state.Set(stateWaiting)
	slog.Info("sendspin waiting for a server", "name", name, "port", Port)
}

func (p *Player) stop() {
	p.mu.Lock()
	cancel := p.running
	p.running = nil
	p.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()

	if err := firewall.Close(firewall.Sendspin); err != nil {
		slog.Warn("closing the sendspin port failed", "port", Port, "err", err)
	}
}
