package detect

import (
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/inference/detect"
	"github.com/ygelfand/libcountertop/pkg/inference/pryon"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/runtime/payload"
	"github.com/ygelfand/libcountertop/pkg/runtime/service"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/diag"
	"github.com/ygelfand/echolocal/internal/feature/voice"
	"github.com/ygelfand/echolocal/internal/feature/wakeword"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/wake"
	"github.com/ygelfand/echolocal/internal/parts"
)

func init() {
	component.Register(sharedcomponent.Device, Get, sharedcomponent.Order(40),
		sharedcomponent.Supervise(service.Restart(time.Second, 30*time.Second)))
}

type Detect struct {
	*detect.Engine
	busy   *wakeBusy
	stop   *detect.StopWord
	pryon  *detect.PryonSwitch
	gemini *detect.GeminiSwitch
}

var (
	once   sync.Once
	shared *Detect
)

func Get() *Detect {
	once.Do(func() { shared = newDetect() })
	return shared
}

var pauses []func() bool

func PauseWhen(f func() bool) { pauses = append(pauses, f) }

func paused() bool {
	for _, f := range pauses {
		if f() {
			return true
		}
	}
	return false
}

func newDetect() *Detect {
	e := detect.New(detect.StopSlot+1, mic.Get())
	e.Paused = paused

	e.Threshold = func(slot int) float64 {
		if slot == detect.StopSlot {
			return config.Get().Wake.Stop.Threshold
		}
		return wakeword.Threshold(slot)
	}
	e.OnDetect = func(slot int) {
		if slot == detect.StopSlot {
			voice.Get().Interrupt()
			return
		}
		voice.Get().Start(slot)
	}
	if helper := parts.Pryon(); helper != nil {
		e.Pryon = func() (pryon.Options, error) {
			if err := payload.Install(layout.PryonHelper, helper); err != nil {
				return pryon.Options{}, err
			}
			return pryon.Options{Helper: layout.PryonHelper, Library: wake.Pryon().Library()}, nil
		}
	}
	d := &Detect{Engine: e, busy: newWakeBusy(led.Get().Busy(), e.Ready)}
	reload := func() {
		wake.Lib().Reload()
		if err := e.Load(); err != nil {
			slog.Error("reloading wake words failed", "err", err)
		}
		component.Reconnect.Emit(struct{}{})
	}
	d.pryon = detect.NewPryonSwitch(e, detect.PryonOptions{
		Read:    func() schema.Wake { return config.Get().Wake },
		Save:    config.Set().Pryon().Set,
		Changed: reload,
	})
	component.Settings.Add(d.pryon.Controls())
	d.gemini = detect.NewGeminiSwitch(e, detect.GeminiOptions{
		Read:    func() bool { return config.Get().Wake.Gemini },
		Save:    config.Set().Gemini().On,
		Changed: reload,
	})
	component.Settings.Add(d.gemini.Controls())
	d.stop = detect.NewStopWord(e, detect.StopOptions{
		Directory: layout.StateDir, DeviceID: component.DeviceMicrophone,
		Read: func() float64 { return config.Get().Wake.Stop.Threshold },
		Save: func(v float64) error { return config.Set().Stop().Threshold(v) },

		PryonOn:    d.pryon.Stop().On,
		PryonModel: wake.Pryon().Stop,
	})
	component.Settings.Add(d.stop.Controls())
	e.OnReady = d.busy.scored

	// Home Assistant pushes a wake word selection only when the user changes one.
	e.Load = func() error {
		turn := voice.Get()
		turn.SetSlots(d.load(turn.Slots()))
		d.stop.Load()
		return nil
	}

	voice.Get().OnWakeWord(d.load, diag.Get().Measure)
	light := &downloadLight{busy: led.Get().Busy()}
	wake.Lib().Preparing.Listen(light.show)

	detect.LogInstalled(wake.Lib().Ours())
	return d
}

func (d *Detect) Entities() []esphome.Entity   { return []esphome.Entity{d.stop.Entity} }
func (d *Detect) Pryon() *detect.PryonSwitch   { return d.pryon }
func (d *Detect) Gemini() *detect.GeminiSwitch { return d.gemini }
func (d *Detect) Restore(c config.Config)      { d.stop.Restore(c.Wake.Stop.Threshold) }

func (d *Detect) load(ids []string) []string {
	d.busy.begin()
	accepted, loaded := detect.Choose(d.Engine, wake.Lib().Ensure(ids), ids, wakeword.Slots)
	d.busy.waitFor(loaded)
	return accepted
}
