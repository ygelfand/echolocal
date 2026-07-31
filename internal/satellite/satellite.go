// Package satellite presents the Dot to Home Assistant over the ESPHome native API.
package satellite

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/mdns"

	"github.com/ygelfand/echolocal/internal/alog"
	"github.com/ygelfand/echolocal/internal/gpio"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/speaker"
	"github.com/ygelfand/echolocal/internal/wake"
)

// Config is what a satellite needs to come up.
type Config struct {
	// Name is the display name, as the user typed it.
	Name string

	Version string
	Addr    string

	// Ring is the driver, not the hardware: everything that wants the ring takes a claim on it, so
	// the satellite is one of several holders rather than the owner.
	Ring    *led.Driver
	Mute    *gpio.Mute
	MuteLED *gpio.MuteLED
	Speaker *speaker.Player

	// Sound decides what the speaker is playing, the same way Ring decides what the ring shows.
	Sound *speaker.Driver

	// Mic is the array. Without it there is nothing to send Home Assistant, so no voice.
	Mic *mic.Source
}

// Satellite is the running server and the entities Home Assistant drives.
type Satellite struct {
	srv *esphome.Server

	// kit is everything the satellite is made of, shared with the pieces themselves.
	kit *kit

	mute    *muteSwitch
	turn    *conversation
	buttons *buttonEvents
	name    string
}

// New builds the server and its entities. It does not listen; call Serve.
func New(cfg Config) (*Satellite, error) {
	psk, err := loadPSK(layout.KeyPath)
	if err != nil {
		return nil, err
	}

	if err := settings.LoadError(); err != nil {
		slog.Error("reading saved settings failed, continuing with defaults", "err", err)
	}

	k := &kit{
		Speaker: cfg.Speaker,
		Mic:     cfg.Mic,
		Mute:    cfg.Mute,
		MuteLED: cfg.MuteLED,
		LEDs:    cfg.Ring,
		Sound:   cfg.Sound,
	}
	ents := esphome.NewEntities()

	k.Ring = newRingLight(k)
	ents.Add(k.Ring.entities()...)

	var mute *muteSwitch
	if k.Mute != nil {
		mute = newMuteSwitch(k)
		ents.Add(mute.entities()...)
	}

	// The ring can be set to follow the room, which needs the light for its colour and the microphone
	// for the room, so it is built after both.
	room := newRoomReaction(k)
	k.Ring.OnColor(room.Recolour)
	ents.Add(room.entities()...)

	k.Player = newMediaPlayer(k)
	ents.Add(k.Player.entities()...)

	k.Wake = newWakeControl(k, WakeSlots)
	ents.Add(k.Wake.entities()...)

	opts := newOptions(k)
	ents.Add(opts.entities()...)

	k.Log = newActivity()
	ents.Add(k.Log.entities()...)

	// The action button drives the conversation, which needs the satellite that is built below.
	s := &Satellite{kit: k, mute: mute}

	s.buttons = newButtonEvents()
	ents.Add(s.buttons.entities()...)

	k.Diag = newDiagnostics(k, s.WakeSlot)
	ents.Add(k.Diag.entities()...)

	mac, err := macAddress()
	if err != nil {
		return nil, err
	}

	node := layout.Slug(cfg.Name)
	srv := &esphome.Server{
		Addr: cfg.Addr,
		Info: esphome.Info{
			Name:         node,
			FriendlyName: cfg.Name,
			MACAddress:   mac,
			Manufacturer: layout.Manufacturer,
			Model:        layout.Model,
			Version:      cfg.Version,
		},
		PSK:    psk,
		Logger: slog.Default(),
		// Persist a key Home Assistant pushes, or the next connection reverts to the old one.
		OnSetEncryptionKey: func(k esphome.PSK) error { return writePSK(layout.KeyPath, k) },
		Handler:            ents,
	}

	s.srv, s.name = srv, node

	// Everything the device remembers, put back in one pass now that the pieces that own it exist. This
	// runs while the boot animation is still up — the splash outranks the ring's own appearance, so a
	// restored light waits underneath it rather than fighting it — and before Home Assistant has
	// connected, because how the device behaves is not its business.
	restore(k, mute, opts, room)

	// Voice needs microphones. Announce and StartConversation go together and both need a
	// media_player.
	if cfg.Mic != nil {
		srv.Info.VoiceFeatures = esphome.DefaultVoiceFeatures |
			esphome.FeatureSpeaker |
			esphome.FeatureAnnounce |
			esphome.FeatureStartConversation
		k.Voice = s.newVoiceSatellite()
		s.turn = newConversation(k)
		srv.Handler = esphome.Chain(ents, k.Voice)
	}
	return s, nil
}

// newVoiceSatellite follows Home Assistant's selection and answers its questions. What the device can
// hear is not stored here: it is asked for on every configuration request, because models arrive and
// are deleted while the device runs.
func (s *Satellite) newVoiceSatellite() *esphome.VoiceSatellite {
	ours := wake.Lib().Ours()

	active := activeWakeWords(ours, WakeSlots)
	vs := &esphome.VoiceSatellite{
		ActiveWakeWords:     active,
		MaxActiveWakeWords:  WakeSlots,
		OnExternalWakeWords: s.answerWakeWords,
	}
	slog.Info("wake words", "ours", len(ours), "active", active)
	return vs
}

// OnWakeWord is called when Home Assistant changes the selection, so the engine can follow. It is
// given every slot: load reports which of them it accepted, and only those are echoed back as
// active, because Home Assistant takes the echo as authoritative and reverts a slot whose word is
// missing from it. A slot the device will not run therefore reverts in the interface rather than
// sitting there looking armed.
func (s *Satellite) OnWakeWord(load func(ids []string) []string) {
	if s.kit.Voice == nil {
		return
	}

	s.kit.Voice.OnSetActiveWakeWords = func(ids []string) {
		accepted := load(ids)
		s.kit.Voice.ActiveWakeWords = accepted

		for slot := range WakeSlots {
			id := ""
			if slot < len(accepted) {
				id = accepted[slot]
			}
			if err := settings.SetWakeWord(slot, id); err != nil {
				slog.Error("saving the wake word failed", "slot", slot+1, "err", err)
			}
		}
		if len(accepted) != len(ids) {
			slog.Warn("some wake words were refused", "asked", ids, "running", accepted)
		}

		// A selection both downloads models and lets go of the ones it replaced, so it is the one
		// thing that moves either number.
		if s.kit.Diag != nil {
			s.kit.Diag.measure()
		}
	}
}

// WakeThreshold is a slot's detection threshold, as set from Home Assistant.
func (s *Satellite) WakeThreshold(slot int) float64 { return s.kit.Wake.Threshold(slot) }

// ActiveWakeWords is what the device is advertising as listening, by slot.
func (s *Satellite) ActiveWakeWords() []string {
	if s.kit.Voice == nil {
		return nil
	}
	return s.kit.Voice.ActiveWakeWords
}

// SetActiveWakeWords corrects what is advertised to what is actually running. The engine loads at
// start-up rather than waiting to be told, so this is how the advertisement is reconciled with what
// came up: anything that failed to load is not claimed.
func (s *Satellite) SetActiveWakeWords(ids []string) {
	if s.kit.Voice == nil {
		return
	}
	s.kit.Voice.ActiveWakeWords = ids
	slog.Info("wake words listening", "active", ids)
}

// Sample publishes the diagnostics that drift on their own: free space, temperatures, cores, load and
// memory. Called from the heartbeat, so they share one timestamp instead of each keeping its own timer.
func (s *Satellite) Sample() { s.kit.Diag.Sample() }

// PipelineReady reports whether Home Assistant has a voice pipeline listening. Wake detection runs
// before that happens, but nothing can be done with a detection until it does, so this is what the
// device shows on the ring while it comes up.
func (s *Satellite) PipelineReady() bool { return s.kit.Voice != nil && s.kit.Voice.Subscribed() }

// RunConversation owns the voice conversation until ctx is cancelled. Nothing happens on a wake word
// until it is running.
func (s *Satellite) RunConversation(ctx context.Context) {
	if s.turn == nil {
		return
	}
	s.turn.Run(ctx)
}

// Action is the action button: it gives up on whatever is happening, or starts something if nothing
// is. Cancelling is the more useful half — it is the way out of a turn that is waiting on a pipeline
// that is not going to answer.
func (s *Satellite) Action() {
	if s.turn == nil {
		slog.Warn("no voice pipeline; the action button has nothing to do")
		chime(s.kit.Sound, toneTrouble)
		return
	}

	if s.turn.Busy() {
		s.turn.Cancel()
		return
	}
	// No wake word, so no slot to pair with: the first pipeline is the one Home Assistant falls back
	// to for anything that reports no phrase.
	s.turn.Start(0)
}

// ActionHold is holding the action button, which reaches the second assistant. Holding does not
// cancel: a press is the way out of a turn, so holding while one is running interrupts it with the
// other assistant instead, which is the same thing saying the other wake word would do.
func (s *Satellite) ActionHold() {
	if s.turn == nil {
		slog.Warn("no voice pipeline; the action button has nothing to hold")
		chime(s.kit.Sound, toneTrouble)
		return
	}
	s.turn.Start(1)
}

// WakeDetected starts a conversation on the pipeline paired with the slot whose wake word fired. What
// that means from the phase the conversation is already in is its decision, not the engine's.
func (s *Satellite) WakeDetected(slot int) { s.WakeSlot(slot) }

// WakeSlot asks for a turn as if that slot's wake word had fired, which is also how the buttons in
// Home Assistant reach a pipeline without anything being said.
func (s *Satellite) WakeSlot(slot int) {
	if s.turn == nil {
		slog.Warn("no voice pipeline; nothing to wake", "slot", slot+1)
		return
	}
	s.turn.Start(slot)
}

// Serve listens until ctx is cancelled, advertising over mDNS so Home Assistant finds the
// device without being told an address.
func (s *Satellite) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("satellite: listen %s: %w", s.srv.Addr, err)
	}

	go alog.Safely("mdns", func() { s.advertise(ctx, ln.Addr().(*net.TCPAddr).Port) })

	return s.srv.Serve(ctx, ln)
}

// advertise publishes the addresses the device actually has, and republishes them when they change.
//
// echod starts from init, well before wifi has associated. The addresses are passed rather than left
// to the library, which falls back to loopback when nothing routable exists — a record Home Assistant
// discovers and then cannot connect to, which is worse than no record at all. Discovery is a
// convenience and a device reachable by address works without it, so this only logs.
func (s *Satellite) advertise(ctx context.Context, port int) {
	for attempt := 1; ; attempt++ {
		ips := routable()
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
			Name:         s.name,
			FriendlyName: s.srv.Info.FriendlyName,
			Port:         port,
			MACAddress:   s.srv.Info.MACAddress,
			Version:      s.srv.Info.Version,
			Platform:     layout.Platform,
			Board:        layout.Board,
			Encrypted:    s.srv.PSK != nil,
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

		slog.Info("advertising over mdns", "name", s.name, "port", port, "addrs", addrKey(ips))

		// The registration stands until the addresses it was made with are no longer the ones the
		// device has: a lease that changed, or a network that arrived late.
		for addrKey(routable()) == addrKey(ips) {
			if !pause(ctx, mdnsRetry) {
				adv.Close()
				return
			}
		}
		adv.Close()
		slog.Info("addresses changed, re-advertising over mdns", "was", addrKey(ips))
	}
}

// routable is what the device can be reached on: no loopback, no link-local.
func routable() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}

	var ips []net.IP
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if ok && ipnet.IP.IsGlobalUnicast() && !ipnet.IP.IsLinkLocalUnicast() {
			ips = append(ips, ipnet.IP)
		}
	}
	return ips
}

// addrKey is a set of addresses as one comparable string, so a change is one comparison.
func addrKey(ips []net.IP) string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	slices.Sort(out)
	return strings.Join(out, ",")
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

// mdnsRetry is how long to wait between attempts. There is no attempt limit: wifi can come back long
// after boot, and an advert that never reappears is a device that has to be found by address.
const mdnsRetry = 3 * time.Second

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
		return nil, fmt.Errorf("satellite: key at %s: %w", path, err)
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
