package sendspin

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/android/firewall"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/safe"
)

func init() {
	component.Register(component.Device, Get(), component.Order(26))
}

// Player is the room's membership of a group, as Home Assistant sees it: a listening port, an advert
// saying the room is here, and the credentials a server pairs with.
type Player struct {
	enabled  *esphome.Switch
	unpaired *esphome.Switch
	state    *esphome.TextSensor
	security *esphome.TextSensor
	token    *esphome.TextSensor
	forget   *esphome.Button

	out *out

	trustOnce sync.Once
	trust     *trust
	trustErr  error

	mu       sync.Mutex
	running  context.CancelFunc
	listener *listener
	wake     chan struct{}

	// leaving is what the room tells a server when the listener stops: the switch going off is the user's
	// doing, anything else is the process going down and coming back.
	leaving atomic.Value
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
	p.leaving.Store(goodbyeRestart)

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

	p.unpaired = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "sendspin_unpaired_access",
			Name:     "Sendspin unpaired access",
			Icon:     "mdi:lock-open-variant-outline",
			Category: esphome.CategoryConfig,
			DeviceID: component.DevicePlayback,
		},
		OnCommand: func(on bool) {
			p.unpaired.Set(on)
			if err := config.Set().Sendspin().UnpairedAccess(on); err != nil {
				slog.Error("saving a setting failed", "setting", p.unpaired.ObjectID, "err", err)
			}
			if !on {
				p.mu.Lock()
				l := p.listener
				p.mu.Unlock()
				if l != nil {
					l.unpairedOff()
				}
			}
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

	p.security = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "sendspin_security",
			Name:     "Sendspin security",
			Icon:     "mdi:shield-lock-outline",
			Category: esphome.CategoryDiagnostic,
			DeviceID: component.DevicePlayback,
		},
	}
	p.security.Set(securityOff)

	// The token is what an operator types into a server to pair it with this room. It is a secret in
	// the way a Wi-Fi password is: whoever holds it can pair. Home Assistant is where the room's owner
	// already is, so it is shown there rather than printed on a leaflet.
	p.token = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "sendspin_pairing_token",
			Name:     "Sendspin pairing token",
			Icon:     "mdi:key-variant",
			Category: esphome.CategoryDiagnostic,
			DeviceID: component.DevicePlayback,
		},
	}

	p.forget = &esphome.Button{
		Base: esphome.Base{
			ObjectID: "sendspin_forget_pairings",
			Name:     "Sendspin forget pairings",
			Icon:     "mdi:link-off",
			Category: esphome.CategoryConfig,
			DeviceID: component.DevicePlayback,
		},
		OnPress: func() {
			tr, err := p.credentials()
			if err != nil {
				return
			}
			if err := tr.forgetAll(); err != nil {
				slog.Error("sendspin forgetting pairings failed", "err", err)
				return
			}
			slog.Info("sendspin pairings forgotten")
		},
	}
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
	return []esphome.Entity{p.enabled, p.unpaired, p.state, p.security, p.token, p.forget}
}

// Restore puts the switches back where they were left and reads the room's credentials, making them
// on a device that has none. Listening waits for Run, once there is a network.
func (p *Player) Restore(c config.Config) {
	p.enabled.Set(c.Sendspin.Enabled)
	p.unpaired.Set(c.Sendspin.UnpairedAccess)
	if _, err := p.credentials(); err != nil {
		slog.Error("sendspin credentials", "err", err)
	}
}

// credentials is the trust file, read once. A device that cannot read it cannot be a Sendspin room:
// every server would see a different device each time.
func (p *Player) credentials() (*trust, error) {
	p.trustOnce.Do(func() {
		p.trust, p.trustErr = loadTrust(layout.SendspinTrustPath)
		if p.trustErr != nil {
			return
		}
		p.token.Set(p.trust.token())
		slog.Info("sendspin identity", "client_id", p.trust.clientID(), "pairings", p.trust.count())
	})
	return p.trust, p.trustErr
}

// Credentials is what echoctl and the tools print for someone pairing by hand: the room's identity and
// the token a server takes.
func Credentials() (clientID, token string, err error) {
	tr, err := loadTrust(layout.SendspinTrustPath)
	if err != nil {
		return "", "", err
	}
	return tr.clientID(), tr.token(), nil
}

// Run holds the port open for as long as the switch is on.
func (p *Player) Run(ctx context.Context) error {
	defer p.stop(goodbyeRestart)

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
		p.stop(goodbyeUserRequest)
		p.state.Set(stateOff)
		p.security.Set(securityOff)
		return
	}

	tr, err := p.credentials()
	if err != nil {
		slog.Error("sendspin cannot start without credentials", "err", err)
		return
	}

	// Until told otherwise, leaving means the process is going down and coming back.
	p.leaving.Store(goodbyeRestart)

	ctx, cancel := context.WithCancel(parent)
	l := newListener(p.out, speaker.Sound().Backgrounds(), tr, p.state.Set, p.security.Set, p.reason)
	p.mu.Lock()
	p.running = cancel
	p.listener = l
	p.mu.Unlock()

	// The vendor's chain drops what it was not told about.
	if err := firewall.Open(firewall.Sendspin, Port); err != nil {
		slog.Error("opening the sendspin port failed", "port", Port, "err", err)
	}

	name := config.Get().Device.Name
	safe.Go("sendspin listen", func() {
		if err := l.serve(ctx, name); err != nil {
			slog.Error("sendspin listener stopped", "err", err)
		}
	})
	safe.Go("sendspin advertise", func() { advertise(ctx, name, Port) })

	p.state.Set(stateWaiting)
	p.security.Set(securityWaiting)
	slog.Info("sendspin waiting for a server", "name", name, "port", Port, "client_id", tr.clientID())
}

// reason is what a session says when the listener stops.
func (p *Player) reason() string { return p.leaving.Load().(string) }

// stop ends the listener, telling any connected server why.
func (p *Player) stop(reason string) {
	p.mu.Lock()
	cancel := p.running
	p.running = nil
	p.listener = nil
	p.mu.Unlock()

	if cancel == nil {
		return
	}
	p.leaving.Store(reason)
	cancel()

	if err := firewall.Close(firewall.Sendspin); err != nil {
		slog.Warn("closing the sendspin port failed", "port", Port, "err", err)
	}
}
