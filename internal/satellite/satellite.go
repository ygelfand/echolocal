// Package satellite presents the Dot to Home Assistant over the ESPHome native API.
package satellite

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/mdns"

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
	Ring    *led.Ring
	Mute    *gpio.Mute
	MuteLED *gpio.MuteLED
	Speaker *speaker.Player

	// Mic is the array. Without it there is nothing to send Home Assistant, so no voice.
	Mic *mic.Source
}

// Satellite is the running server and the entities Home Assistant drives.
type Satellite struct {
	srv     *esphome.Server
	ring    *ringLight
	mute    *muteSwitch
	player  *mediaPlayer
	wake    *wakeControl
	voice   *esphome.VoiceSatellite
	turn    *voiceTurn
	buttons map[uint16]*button
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

	ring := newRingLight(cfg.Ring)
	ents := esphome.NewEntities()
	ents.Add(ring.entities()...)

	var mute *muteSwitch
	if cfg.Mute != nil {
		mute = newMuteSwitch(cfg.Mute, cfg.MuteLED, cfg.Speaker)
		ents.Add(mute.entities()...)
	}

	player := newMediaPlayer(ring, cfg.Speaker)
	ents.Add(player.entities()...)

	wakeCtl := newWakeControl(ring, cfg.Speaker, wake.Backends(models), WakeSlots)
	ents.Add(wakeCtl.entities()...)

	ents.Add(newOptions(cfg.Mic, cfg.Speaker).entities()...)

	// The action button starts a conversation, which needs the satellite that is built below.
	s := &Satellite{ring: ring, mute: mute, player: player, wake: wakeCtl, models: models}

	buttons := newButtons(player, mute, s.StartConversation)
	for _, b := range buttons {
		ents.Add(b.event)
	}

	ents.Add(newDiagnostics(cfg.Speaker, s.WakeSlot).entities()...)

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

	s.srv, s.buttons, s.name = srv, buttons, node

	// Voice needs microphones. Announce and StartConversation go together and both need a
	// media_player.
	if cfg.Mic != nil {
		srv.Info.VoiceFeatures = esphome.DefaultVoiceFeatures |
			esphome.FeatureSpeaker |
			esphome.FeatureAnnounce |
			esphome.FeatureStartConversation
		s.voice = newVoiceSatellite(s.backendModels())

		// The turn's animation is the one the wake word that started it is set to.
		s.turn = newVoiceTurn(s.voice, cfg.Mic, cfg.Speaker, ring, player,
			func() string { return s.wake.Effect(s.turn.slot()) })
		srv.Handler = esphome.Chain(ents, s.voice)
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
	if s.voice == nil {
		return
	}

	s.voice.OnSetActiveWakeWords = func(ids []string) {
		accepted := load(ids)
		s.voice.ActiveWakeWords = accepted

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
func (s *Satellite) WakeThreshold(slot int) float64 { return s.wake.Threshold(slot) }

// ActiveWakeWords is what the device is advertising as listening, by slot.
func (s *Satellite) ActiveWakeWords() []string {
	if s.voice == nil {
		return nil
	}
	return s.voice.ActiveWakeWords
}

// SetActiveWakeWords corrects what is advertised to what is actually running. The engine loads at
// start-up rather than waiting to be told, so this is how the advertisement is reconciled with what
// came up: anything that failed to load is not claimed.
func (s *Satellite) SetActiveWakeWords(ids []string) {
	if s.voice == nil {
		return
	}
	s.voice.ActiveWakeWords = ids
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
	if s.voice == nil {
		return
	}

	s.wake.onBackend = func(b settings.WakeBackend) {
		active := reload(b)

		available, fallback := wakeWords(s.backendModels(), WakeSlots)
		if len(active) == 0 {
			active = fallback
		}
		s.voice.AvailableWakeWords = available
		s.voice.ActiveWakeWords = active

		slog.Info("re-advertising wake words", "backend", b, "count", len(available), "active", active)
		s.srv.Reconnect()
	}
}

// PipelineReady reports whether Home Assistant has a voice pipeline listening. Wake detection runs
// before that happens, but nothing can be done with a detection until it does, so this is what the
// device shows on the ring while it comes up.
func (s *Satellite) PipelineReady() bool { return s.voice != nil && s.voice.Subscribed() }

// ReleaseRing hands the ring to the light entity, applying whatever it was told while the boot
// animation had it. The ring is held from the moment the satellite is built, so this is the only
// half of the pair a caller needs.
func (s *Satellite) ReleaseRing() { s.ring.release() }

// StartConversation opens a turn without a wake word, for the action button.
func (s *Satellite) StartConversation() {
	if s.turn == nil {
		slog.Warn("no voice pipeline; the action button has nothing to start")
		return
	}
	// No wake word, so no slot to pair with: the first pipeline is the one Home Assistant falls back
	// to for anything that reports no phrase.
	s.turn.Start(0, "")
}

// WakeDetected shows and sounds a detection in one of Home Assistant's slots, then starts a
// conversation on the pipeline that slot is paired with. The engine calls it.
func (s *Satellite) WakeDetected(slot int) {
	// Saying the wake word while the device is still listening is part of the sentence, not a new
	// request: acting on it would cut off what the user is in the middle of saying. Once the
	// pipeline is replying, a wake word is a deliberate interruption and does start a turn.
	// Detection keeps running throughout either way, because a detector starved of audio scores
	// the next utterance from a cold state.
	if s.turn != nil && s.turn.sending() {
		slog.Info("wake word ignored, still listening")
		return
	}

	phrase, _ := s.phraseFor(slot)
	slog.Info("wake word detected", "slot", slot+1, "phrase", phrase)
	s.startWake(slot, phrase)
}

// WakeSlot starts a turn as if the wake word in one of Home Assistant's slots had fired, for trying
// a pipeline without saying anything, or for waking the device by hand.
func (s *Satellite) WakeSlot(slot int) {
	phrase, ok := s.phraseFor(slot)
	if !ok {
		slog.Warn("nothing to wake: no wake word in that slot", "slot", slot+1)
		return
	}
	slog.Info("wake requested", "slot", slot+1, "phrase", phrase)
	s.startWake(slot, phrase)
}

// startWake is the detection itself, once the phrase to report is known. The tone and the animation
// are the slot's own.
func (s *Satellite) startWake(slot int, phrase string) {
	s.wake.Chime(slot)

	// The animation starts on the wake either way. A turn stops it when it ends; without one there
	// is nothing to stop it, so it gets a duration instead.
	if s.turn == nil {
		s.wake.Flash(slot)
		return
	}
	s.wake.Hold(slot)

	if !s.turn.Start(slot, phrase) {
		s.ring.still()
		s.ring.restore()
	}
}

// phraseFor is what Home Assistant expects a turn to report for one of its wake word slots: the
// spoken phrase, not the model's id. Which pipeline runs is resolved by comparing that phrase
// against each slot's select, so a turn from slot n has to report slot n's own phrase.
func (s *Satellite) phraseFor(slot int) (string, bool) {
	if slot < 0 || slot >= len(s.voice.ActiveWakeWords) {
		return "", false
	}

	id := s.voice.ActiveWakeWords[slot]
	for _, w := range s.voice.AvailableWakeWords {
		if w.ID == id {
			return w.Phrase, true
		}
	}
	return id, true
}

// Serve listens until ctx is cancelled, advertising over mDNS so Home Assistant finds the
// device without being told an address.
func (s *Satellite) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("satellite: listen %s: %w", s.srv.Addr, err)
	}

	watchButtons(ctx, s.buttons)

	adv, err := mdns.Advertise(mdns.Config{
		Name:         s.name,
		FriendlyName: s.srv.Info.FriendlyName,
		Port:         ln.Addr().(*net.TCPAddr).Port,
		MACAddress:   s.srv.Info.MACAddress,
		Version:      s.srv.Info.Version,
		Platform:     layout.Platform,
		Board:        layout.Board,
		Encrypted:    s.srv.PSK != nil,
	})
	if err != nil {
		// Discovery is a convenience; a device reachable by address still works.
		slog.Warn("mdns advertise failed", "err", err)
	} else {
		defer adv.Close()
	}

	return s.srv.Serve(ctx, ln)
}

// ringLight maps Home Assistant's light entities onto the 12-segment ring: one light for the
// whole ring, plus a light per segment for people who want them.
//
// Everything that writes the ring goes through here, because an animation and a command
// writing frames at the same time produce nonsense. Starting anything cancels what was running.
type ringLight struct {
	light *esphome.Light
	segs  []*esphome.Light
	ring  *led.Ring

	mu     sync.Mutex
	cancel context.CancelFunc
	frame  []led.Color

	// booting means the boot animation owns the ring. Home Assistant syncs entity state as soon as
	// it connects, which lands before it subscribes a pipeline, and the light entity restoring its
	// saved effect would otherwise start a second animation over the top of the first.
	booting bool
}

func newRingLight(ring *led.Ring) *ringLight {
	r := &ringLight{
		// Held from birth. Restoring the saved volume paints a white arc while the satellite is
		// still being built, and Home Assistant syncs entity state as soon as it connects — both
		// land in the middle of the boot animation unless the ring is withheld until it is done.
		booting: true,
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "ring", Name: "LED ring"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeRGB},
			Effects:             led.EffectNames(),
		},
		ring:  ring,
		frame: make([]led.Color, led.Segments),
	}
	r.light.OnCommand = r.apply

	// Start from white at full brightness so the first command has something to turn on.
	r.light.Set(esphome.LightState{
		ColorMode:  esphome.ColorModeRGB,
		Brightness: 1, Red: 1, Green: 1, Blue: 1,
	})

	for i := range led.Segments {
		seg := &esphome.Light{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("segment_%d", i+1),
				Name:     fmt.Sprintf("LED ring segment %d", i+1),
				// Twelve extra entities is a lot for anyone who just wants the ring.
				DisabledByDefault: true,
			},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeRGB},
		}
		seg.OnCommand = func(s esphome.LightState) { r.applySegment(i, seg, s) }
		r.segs = append(r.segs, seg)
	}
	return r
}

// entities lists everything the ring exposes.
func (r *ringLight) entities() []esphome.Entity {
	out := []esphome.Entity{r.light}
	for _, s := range r.segs {
		out = append(out, s)
	}
	return out
}

// animate replaces whatever is driving the ring with f, which runs until cancelled. While the boot
// animation owns the ring, nothing else gets to drive it.
func (r *ringLight) animate(f func(context.Context)) {
	r.mu.Lock()
	if r.booting {
		r.mu.Unlock()
		return
	}
	if r.cancel != nil {
		r.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.mu.Unlock()

	go f(ctx)
}

// hold gives the ring to the boot animation. Entity state is still tracked while it is held, so
// Home Assistant sees the right values; only the writes are withheld.
func (r *ringLight) hold() {
	r.mu.Lock()
	r.booting = true
	r.mu.Unlock()
}

// release hands the ring back and applies whatever state arrived while it was held.
func (r *ringLight) release() {
	r.mu.Lock()
	was := r.booting
	r.booting = false
	r.mu.Unlock()

	if was {
		r.restore()
	}
}

// Flash shows something briefly and then puts back whatever the ring was showing, so a volume
// arc does not leave the ring stuck on it or drop an effect that was running.
func (r *ringLight) Flash(frame []led.Color, d time.Duration) {
	r.animate(func(ctx context.Context) {
		if err := r.ring.SetSegments(frame); err != nil {
			slog.Error("flash write failed", "err", err)
			return
		}
		select {
		case <-ctx.Done():
			// Something else took the ring; it owns what happens next.
			return
		case <-time.After(d):
		}
		r.restore()
	})
}

// colorOf is the light's colour with its brightness folded in.
func colorOf(s esphome.LightState) led.Color {
	return led.Color{R: scale(s.Red, s.Brightness), G: scale(s.Green, s.Brightness), B: scale(s.Blue, s.Brightness)}
}

// HoldEffect runs an effect until something else takes the ring. Nothing restores afterwards: the
// caller decides when it is over.
func (r *ringLight) HoldEffect(effect string) {
	base := colorOf(r.light.Get())

	r.animate(func(ctx context.Context) {
		if err := led.RunEffect(ctx, r.ring, effect, base); err != nil && ctx.Err() == nil {
			slog.Error("effect failed", "effect", effect, "err", err)
		}
	})
}

// HoldEffectReversed is HoldEffect the other way round the ring, for when the device has stopped
// listening and is waiting on an answer.
func (r *ringLight) HoldEffectReversed(effect string) {
	base := colorOf(r.light.Get())

	r.animate(func(ctx context.Context) {
		if err := led.RunEffectReversed(ctx, r.ring, effect, base); err != nil && ctx.Err() == nil {
			slog.Error("effect failed", "effect", effect, "err", err)
		}
	})
}

// FlashEffect runs an effect for d and then puts back whatever the ring was showing.
func (r *ringLight) FlashEffect(effect string, d time.Duration) {
	base := colorOf(r.light.Get())

	r.animate(func(ctx context.Context) {
		done, cancel := context.WithTimeout(ctx, d)
		defer cancel()

		if err := led.RunEffect(done, r.ring, effect, base); err != nil && ctx.Err() == nil {
			slog.Error("effect failed", "effect", effect, "err", err)
		}
		if ctx.Err() != nil {
			return
		}
		r.restore()
	})
}

// restore re-applies the light's own state, which is the ring's resting appearance.
func (r *ringLight) restore() {
	s := r.light.Get()
	if s.On && s.Effect != "" && s.Effect != "None" {
		r.apply(s)
		return
	}
	if err := r.paint(s); err != nil {
		slog.Error("ring restore failed", "err", err)
	}
}

// still stops any animation, leaving the ring as it is.
func (r *ringLight) still() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
}

func (r *ringLight) apply(s esphome.LightState) {
	s = usable(s)

	if s.On && s.Effect != "" && s.Effect != "None" {
		base := colorOf(s)
		effect := s.Effect
		r.animate(func(ctx context.Context) {
			if err := led.RunEffect(ctx, r.ring, effect, base); err != nil && ctx.Err() == nil {
				slog.Error("effect failed", "effect", effect, "err", err)
			}
		})
		r.light.Set(s)
		return
	}

	r.still()
	if err := r.paint(s); err != nil {
		slog.Error("ring write failed", "err", err)
		return
	}
	r.light.Set(s)
}

// applySegment paints one segment, leaving the others as they are.
func (r *ringLight) applySegment(i int, seg *esphome.Light, s esphome.LightState) {
	s = usable(s)
	r.still()

	r.mu.Lock()
	if s.On {
		r.frame[i] = led.Color{R: scale(s.Red, s.Brightness), G: scale(s.Green, s.Brightness), B: scale(s.Blue, s.Brightness)}
	} else {
		r.frame[i] = led.Color{}
	}
	frame := append([]led.Color(nil), r.frame...)
	r.mu.Unlock()

	if err := r.ring.SetSegments(frame); err != nil {
		slog.Error("segment write failed", "segment", i, "err", err)
		return
	}
	seg.Set(s)
}

func scale(v, brightness float32) byte {
	return byte(math.Round(float64(v) * float64(brightness) * 255))
}

// usable fills in what a bare on command leaves out. Commands are partial and folded onto
// current state, so "on" with no brightness or colour would otherwise light the ring black.
func usable(s esphome.LightState) esphome.LightState {
	if !s.On {
		return s
	}
	if s.Brightness == 0 {
		s.Brightness = 1
	}
	if s.Red == 0 && s.Green == 0 && s.Blue == 0 {
		s.Red, s.Green, s.Blue = 1, 1, 1
	}
	if s.ColorMode == 0 {
		s.ColorMode = esphome.ColorModeRGB
	}
	return s
}

func (r *ringLight) paint(s esphome.LightState) error {
	c := led.Color{}
	if s.On {
		c = led.Color{R: scale(s.Red, s.Brightness), G: scale(s.Green, s.Brightness), B: scale(s.Blue, s.Brightness)}
	}

	r.mu.Lock()
	for i := range r.frame {
		r.frame[i] = c
	}
	booting := r.booting
	r.mu.Unlock()

	if booting {
		return nil
	}
	return r.ring.SetAll(c)
}

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
