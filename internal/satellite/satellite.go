// Package satellite presents the Dot to Home Assistant over the ESPHome native API.
package satellite

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
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

	// models is everything installed, of either backend. What is advertised is filtered from it.
	models []wake.Model
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

	// The installed models decide which backends can be offered, so they are read before anything
	// that shows a choice.
	models, err := wake.Installed(layout.ModelDir)
	if err != nil {
		slog.Warn("no wake word models to advertise", "err", err)
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

	k.Wake = newWakeControl(k, wake.Backends(models), WakeSlots)
	ents.Add(k.Wake.entities()...)

	ents.Add(newOptions(k).entities()...)

	k.Log = newActivity()
	ents.Add(k.Log.entities()...)

	// The action button drives the conversation, which needs the satellite that is built below.
	s := &Satellite{kit: k, mute: mute, models: models}

	s.buttons = newButtonEvents()
	ents.Add(s.buttons.entities()...)

	ents.Add(newDiagnostics(k, s.WakeSlot).entities()...)

	node := layout.Slug(cfg.Name)
	srv := &esphome.Server{
		Addr: cfg.Addr,
		Info: esphome.Info{
			Name:         node,
			FriendlyName: cfg.Name,
			MACAddress:   macAddress(),
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

	// Voice needs microphones. Announce and StartConversation go together and both need a
	// media_player.
	if cfg.Mic != nil {
		srv.Info.VoiceFeatures = esphome.DefaultVoiceFeatures |
			esphome.FeatureSpeaker |
			esphome.FeatureAnnounce |
			esphome.FeatureStartConversation
		k.Voice = newVoiceSatellite(s.backendModels())
		s.turn = newConversation(k)
		srv.Handler = esphome.Chain(ents, k.Voice)
	}
	return s, nil
}

// backendModels is what the selected backend can run.
func (s *Satellite) backendModels() []wake.Model {
	return wake.OfKind(s.models, settings.Get().Wake.BackendOr(settings.DefaultBackend))
}

// newVoiceSatellite advertises what the selected backend can run and follows Home Assistant's
// selection.
func newVoiceSatellite(models []wake.Model) *esphome.VoiceSatellite {
	available, active := wakeWords(models, WakeSlots)
	vs := &esphome.VoiceSatellite{
		AvailableWakeWords: available,
		ActiveWakeWords:    active,
		MaxActiveWakeWords: WakeSlots,
	}
	slog.Info("advertising wake words", "count", len(available), "active", active)
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

// OnWakeBackend is called when the user changes engines. reload swaps the engine over and reports
// the wake words it managed to bring up, which become what is advertised as active.
//
// Home Assistant only re-reads the available wake words when it sets them or when it connects, and
// there is no way to push. Since the two engines offer different models, the connection is dropped
// so the integration reconnects and asks again — otherwise its pickers would go on offering the old
// engine's models until the next restart.
func (s *Satellite) OnWakeBackend(reload func(settings.WakeBackend) []string) {
	if s.kit.Voice == nil {
		return
	}

	s.kit.Wake.onBackend = func(b settings.WakeBackend) {
		active := reload(b)

		available, fallback := wakeWords(s.backendModels(), WakeSlots)
		if len(active) == 0 {
			active = fallback
		}
		s.kit.Voice.AvailableWakeWords = available
		s.kit.Voice.ActiveWakeWords = active

		slog.Info("re-advertising wake words", "backend", b, "count", len(available), "active", active)
		s.srv.Reconnect()
	}
}

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

// advertise keeps trying until it works. echod starts from init, well before wifi has associated, so
// the first attempt on a cold boot fails with no usable interface — registering needs one with an
// address. Discovery is a convenience and a device reachable by address works without it, which is
// why this only logs, but giving up after one go means a device is undiscoverable for the rest of the
// run over a few seconds of boot ordering.
func (s *Satellite) advertise(ctx context.Context, port int) {
	for attempt := 1; ; attempt++ {
		adv, err := mdns.Advertise(mdns.Config{
			Name:         s.name,
			FriendlyName: s.srv.Info.FriendlyName,
			Port:         port,
			MACAddress:   s.srv.Info.MACAddress,
			Version:      s.srv.Info.Version,
			Platform:     layout.Platform,
			Board:        layout.Board,
			Encrypted:    s.srv.PSK != nil,
		})
		if err == nil {
			slog.Info("advertising over mdns", "name", s.name, "port", port, "attempts", attempt)
			<-ctx.Done()
			adv.Close()
			return
		}

		// Only the first failure is worth a warning: after that it is the expected state of a device
		// waiting for its network, and saying so every few seconds buries everything else.
		if attempt == 1 {
			slog.Warn("mdns advertise failed, retrying", "err", err)
		} else {
			slog.Debug("mdns advertise failed", "attempt", attempt, "err", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(mdnsRetry):
		}
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

// macAddress reports wlan0's address, which Home Assistant uses to recognise the device across
// address changes. The interface does not exist yet when echod starts on a cold boot, so the
// remembered value stands in and a watcher saves the real one for next time.
func macAddress() string {
	saved := settings.Get().MAC

	if mac := readMAC(); mac != "" {
		if mac != saved {
			_ = settings.SetMAC(mac)
		}
		return mac
	}

	go rememberMAC()

	if saved != "" {
		return saved
	}
	return "00:00:00:00:00:00"
}

// rememberMAC waits for the interface to come up and saves its address.
func rememberMAC() {
	for range macAttempts {
		time.Sleep(macRetry)
		if mac := readMAC(); mac != "" {
			_ = settings.SetMAC(mac)
			return
		}
	}
}

const (
	macRetry    = 2 * time.Second
	macAttempts = 30
)

func readMAC() string {
	b, err := os.ReadFile(layout.MACPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
