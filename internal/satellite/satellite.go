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
	Logger  *slog.Logger
}

// Satellite is the running server and the entities Home Assistant drives.
type Satellite struct {
	srv     *esphome.Server
	ring    *ringLight
	mute    *muteSwitch
	volume  *volumeControl
	buttons map[uint16]*button
	log     *slog.Logger
	name    string
}

// New builds the server and its entities. It does not listen; call Serve.
func New(cfg Config) (*Satellite, error) {
	psk, err := loadPSK(layout.KeyPath)
	if err != nil {
		return nil, err
	}

	ring := newRingLight(cfg.Ring, cfg.Logger)
	ents := esphome.NewEntities()
	ents.Add(ring.entities()...)

	var mute *muteSwitch
	if cfg.Mute != nil {
		mute = newMuteSwitch(cfg.Mute, cfg.MuteLED, cfg.Logger)
		ents.Add(mute.entities()...)
	}

	volume := newVolumeControl(ring, cfg.Logger)
	ents.Add(volume.num)

	buttons := newButtons(volume, mute)
	for _, b := range buttons {
		ents.Add(b.event)
	}

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
		Logger: cfg.Logger,
		// Persist a key Home Assistant pushes, or the next connection reverts to the old one.
		OnSetEncryptionKey: func(k esphome.PSK) error { return writePSK(layout.KeyPath, k) },
		Handler:            ents,
	}
	return &Satellite{
		srv: srv, ring: ring, mute: mute, volume: volume, buttons: buttons,
		log: cfg.Logger, name: node,
	}, nil
}

// Splash runs the boot animation on the ring. It returns immediately; a command from Home
// Assistant replaces it.
func (s *Satellite) Splash(d time.Duration) { s.ring.Splash(d) }

// Serve listens until ctx is cancelled, advertising over mDNS so Home Assistant finds the
// device without being told an address.
func (s *Satellite) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("satellite: listen %s: %w", s.srv.Addr, err)
	}

	watchButtons(ctx, s.log, s.buttons)

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
		s.log.Warn("mdns advertise failed", "err", err)
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
	log   *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	frame  []led.Color
}

func newRingLight(ring *led.Ring, log *slog.Logger) *ringLight {
	r := &ringLight{
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "ring", Name: "LED Ring"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeRGB},
			Effects:             led.EffectNames(),
		},
		ring:  ring,
		log:   log,
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
				Name:     fmt.Sprintf("Segment %d", i+1),
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

// animate replaces whatever is driving the ring with f, which runs until cancelled.
func (r *ringLight) animate(f func(context.Context)) {
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.mu.Unlock()

	go f(ctx)
}

// Flash shows something briefly and then puts back whatever the ring was showing, so a volume
// arc does not leave the ring stuck on it or drop an effect that was running.
func (r *ringLight) Flash(frame []led.Color, d time.Duration) {
	r.animate(func(ctx context.Context) {
		if err := r.ring.SetSegments(frame); err != nil {
			r.log.Error("flash write failed", "err", err)
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

// restore re-applies the light's own state, which is the ring's resting appearance.
func (r *ringLight) restore() {
	s := r.light.Get()
	if s.On && s.Effect != "" && s.Effect != "None" {
		r.apply(s)
		return
	}
	if err := r.paint(s); err != nil {
		r.log.Error("ring restore failed", "err", err)
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

// Splash runs the boot animation without blocking, so the ring says "started" while echod gets
// on with everything else. A command from Home Assistant cuts it short.
func (r *ringLight) Splash(d time.Duration) {
	r.animate(func(ctx context.Context) {
		if err := led.Splash(ctx, r.ring, d); err != nil && ctx.Err() == nil {
			r.log.Error("splash failed", "err", err)
		}
	})
}

func (r *ringLight) apply(s esphome.LightState) {
	s = usable(s)

	if s.On && s.Effect != "" && s.Effect != "None" {
		base := led.Color{R: scale(s.Red, s.Brightness), G: scale(s.Green, s.Brightness), B: scale(s.Blue, s.Brightness)}
		effect := s.Effect
		r.animate(func(ctx context.Context) {
			if err := led.RunEffect(ctx, r.ring, effect, base); err != nil && ctx.Err() == nil {
				r.log.Error("effect failed", "effect", effect, "err", err)
			}
		})
		r.light.Set(s)
		return
	}

	r.still()
	if err := r.paint(s); err != nil {
		r.log.Error("ring write failed", "err", err)
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
		r.log.Error("segment write failed", "segment", i, "err", err)
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
	r.mu.Unlock()

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
// address changes.
func macAddress() string {
	b, err := os.ReadFile(layout.MACPath)
	if err != nil {
		return "00:00:00:00:00:00"
	}
	return strings.TrimSpace(string(b))
}
