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
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/safe"
)

func init() {
	component.Register(component.Device, Get, component.Order(26))
}

// Player is the room's membership of a group, as Home Assistant sees it: a listening port, an advert
// saying the room is here, the credentials a server pairs with, and what the group is playing.
type Player struct {
	enabled  *esphome.Switch
	unpaired *esphome.Switch
	state    *esphome.TextSensor
	security *esphome.TextSensor
	token    *esphome.TextSensor
	forget   *esphome.Button
	title    *esphome.TextSensor
	artist   *esphome.TextSensor

	out *out

	trustOnce sync.Once
	trust     *trust
	trustErr  error

	mu       sync.Mutex
	running  context.CancelFunc
	listener *listener
	wake     chan struct{}

	// joined is the server holding the room, set while one is connected. Home Assistant's transport
	// controls go to it, and there is nothing to send them to when it is nil.
	joined *session

	// playing is the group's own playback state, which is the only thing that tells a pause from a
	// track that ended: both leave this room silent.
	playing track
	paused  bool

	// artwork is the newest image the server sent. Nothing here can draw it yet.
	artwork []byte

	// leaving is what the room tells a server when the listener stops: the switch going off is the user's
	// doing, anything else is the process going down and coming back.
	leaving atomic.Value
}

// artworkSize is what the server is asked to scale album art to. A placeholder until there is a
// screen to size it to.
const artworkSize = 512

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

	p.title = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "sendspin_title",
			Name:     "Now playing",
			Icon:     "mdi:music-note",
			DeviceID: component.DevicePlayback,
		},
	}
	p.artist = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "sendspin_artist",
			Name:     "Artist",
			Icon:     "mdi:account-music",
			DeviceID: component.DevicePlayback,
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
	return []esphome.Entity{p.enabled, p.unpaired, p.state, p.security, p.token, p.forget, p.title, p.artist}
}

// Play, Pause and Stop implement media.Source: Home Assistant reaches for the speaker entity whoever
// started the audio, so when a group is playing these are what its buttons mean.
//
// Stop falls back to pause because a room that joined a group cannot end what the group is playing;
// leaving the group would silence this room and keep the rest going, which is not what stop means.
func (p *Player) Play()  { p.tell("play") }
func (p *Player) Pause() { p.tell("pause") }
func (p *Player) Stop()  { p.tell("stop", "pause") }

func (p *Player) tell(want ...string) {
	p.mu.Lock()
	s := p.joined
	p.mu.Unlock()

	if s == nil {
		return
	}

	// Off the caller's thread: this arrives on Home Assistant's read loop, and the send holds a lock
	// around a websocket write. A server that stopped reading would take the device's own connection
	// down with it.
	safe.Go("sendspin command", func() { s.tell(want...) })
}

// Playing implements media.Source.
func (p *Player) Playing() (playing, paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state.Get() == statePlaying && !p.paused, p.paused
}

// holds says which session owns the room, and nil when none does. The media player follows it: the
// group answers Home Assistant's transport controls for as long as it is connected, whether or not
// audio happens to be arriving this second.
func (p *Player) holds(s *session) {
	p.mu.Lock()
	p.joined = s
	p.paused = false
	p.playing = track{}
	p.mu.Unlock()

	p.title.Set("")
	p.artist.Set("")

	if s == nil {
		media.Get().External(nil)
		return
	}
	media.Get().External(p)
}

// setState publishes it and tells the media player, which reads Playing from it.
func (p *Player) setState(state string) {
	p.state.Set(state)
	media.Get().Changed()
}

// setSecurity publishes what the connection is running on.
func (p *Player) setSecurity(state string) { p.security.Set(state) }

// grouped takes the group's playback state, which is what separates a pause from a track ending.
func (p *Player) grouped(state string) {
	p.mu.Lock()
	p.paused = state == "paused"
	p.mu.Unlock()
	media.Get().Changed()
}

// track is what the room is playing, as far as the metadata role has said.
func (p *Player) track() track {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing
}

// plays publishes the track. The empty one clears the sensors rather than leaving them naming
// something nobody can hear.
func (p *Player) plays(t track) {
	p.mu.Lock()
	p.playing = t
	p.mu.Unlock()

	p.title.Set(t.Title)
	p.artist.Set(t.Artist)
	if !t.empty() {
		slog.Info("sendspin now playing", "title", t.Title, "artist", t.Artist, "album", t.Album)
	}
}

// drew keeps the newest album art. There is no screen on this device, so this is where it stops.
func (p *Player) drew(channel int, data []byte) {
	p.mu.Lock()
	p.artwork = data
	p.mu.Unlock()
	slog.Debug("sendspin artwork", "channel", channel, "bytes", len(data))
}

// Artwork is the newest album art the server sent, and nil when there is none.
func (p *Player) Artwork() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.artwork
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
	l := newListener(p.out, speaker.Sound().Backgrounds(), tr, p)
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
