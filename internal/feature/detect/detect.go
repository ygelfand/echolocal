package detect

import (
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/inference/detect"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/runtime/service"

	model "github.com/ygelfand/libcountertop/pkg/inference/wake"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/diag"
	"github.com/ygelfand/echolocal/internal/feature/voice"
	"github.com/ygelfand/echolocal/internal/feature/wakeobserve"
	"github.com/ygelfand/echolocal/internal/feature/wakeword"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/wake"
)

func init() {
	component.Register(sharedcomponent.Device, Get, sharedcomponent.Order(40),
		sharedcomponent.Supervise(service.Restart(time.Second, 30*time.Second)))
}

type Detect struct {
	*detect.Engine
	busy *wakeBusy
	stop *detect.StopWord
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
	obs := wakeobserve.Get().Recorder
	e.OnFrame = func(frame []int16, paused bool, dropped uint64) {
		obs.Frame(frame, paused || config.Get().Microphone.Muted, dropped)
	}
	e.OnScore = obs.Score
	e.OnPeak = obs.Peak
	e.OnModel = func(slot int, m model.Model) {
		obs.Model(
			slot,
			m.ID,
			string(m.Kind),
			m.Path,
			m.Config.SlidingWindowSize,
			m.Config.FeaturesStepMs,
		)
	}

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

	d := &Detect{Engine: e, busy: newWakeBusy(led.Get().Busy(), e.Ready)}
	d.stop = detect.NewStopWord(e, detect.StopOptions{
		Directory: layout.StateDir, DeviceID: component.DeviceMicrophone,
		Read: func() float64 { return config.Get().Wake.Stop.Threshold },
		Save: func(v float64) error { return config.Set().Stop().Threshold(v) },
	})
	e.OnReady = d.busy.scored

	// Home Assistant pushes a wake word selection only when the user changes one.
	e.Load = func() error {
		turn := voice.Get()
		turn.SetSlots(d.load(turn.Slots()))
		d.stop.Load()
		return nil
	}

	voice.Get().OnWakeWord(d.load, diag.Get().Measure)

	detect.LogInstalled(wake.Lib().Ours())
	return d
}

func (d *Detect) Entities() []esphome.Entity { return []esphome.Entity{d.stop.Entity} }
func (d *Detect) Restore(c config.Config)    { d.stop.Restore(c.Wake.Stop.Threshold) }

func (d *Detect) load(ids []string) []string {
	d.busy.begin()
	accepted, loaded := detect.Choose(d.Engine, wake.Lib().Ensure(ids), ids, wakeword.Slots)
	d.busy.waitFor(loaded)
	return accepted
}
