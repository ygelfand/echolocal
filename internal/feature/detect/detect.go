package detect

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/feature/diag"
	"github.com/ygelfand/echolocal/internal/feature/voice"
	"github.com/ygelfand/echolocal/internal/feature/wakeword"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
	"github.com/ygelfand/echolocal/internal/lib/wake"
	"github.com/ygelfand/echolocal/internal/service"
)

func init() {
	// Before the API, so Home Assistant cannot read the wake words while they are still loading and be
	// told about one that then fails.
	component.Register(component.Device, Get(), component.Order(40),
		component.Supervise(service.Restart(time.Second, 30*time.Second)))
}

// Detect is the engine as the device runs it: the wake words the user chose, loaded into it, and the
// ring saying so until they can hear.
type Detect struct {
	engine *Engine
	busy   *wakeBusy
}

var (
	once   sync.Once
	shared *Detect
)

func Get() *Detect {
	once.Do(func() { shared = newDetect() })
	return shared
}

func newDetect() *Detect {
	e := New(wakeword.Slots, mic.Get())
	e.Threshold = wakeword.Threshold
	e.OnDetect = voice.Get().Start

	d := &Detect{engine: e, busy: newWakeBusy(led.Get().Busy(), e.Ready)}
	e.OnReady = d.busy.scored

	// The engine loads on every start, including a restart. Home Assistant only pushes a selection when
	// the user changes one, so an engine that came back empty would leave the device deaf while it went
	// on advertising wake words it was not listening for.
	e.Load = func() error {
		turn := voice.Get()
		turn.SetActiveWakeWords(d.load(turn.ActiveWakeWords()))
		return nil
	}

	// A selection both downloads models and lets go of the ones it replaced, so it is the one thing that
	// moves either number the disk reports.
	voice.Get().OnWakeWord(d.load, diag.Get().Measure)

	ours := wake.Lib().Ours()
	slog.Info("wake words installed", "count", len(ours),
		"openwakeword", len(wake.OfKind(ours, wake.KindOpenWakeWord)),
		"microwakeword", len(wake.OfKind(ours, wake.KindMicroWakeWord)))

	return d
}

func (d *Detect) Name() string { return "wake" }

func (d *Detect) Start(ctx context.Context) error { return d.engine.Start(ctx) }

func (d *Detect) Run(ctx context.Context) error { return d.engine.Run(ctx) }

func (d *Detect) Close() error { return d.engine.Close() }

// load puts one wake word in each slot and reports the ids that came up. Whatever the engine refuses
// is left out, so Home Assistant reverts that slot rather than showing a wake word the device is not
// listening for.
func (d *Detect) load(ids []string) []string {
	d.busy.begin()

	// A selection may name a model Home Assistant is offering but this device has never had, so the
	// library is asked rather than a list captured at boot: this is where a new word arrives.
	models := wake.Lib().Ensure(ids)

	var accepted []string
	var loaded []int
	for slot := range wakeword.Slots {
		if slot >= len(ids) || ids[slot] == "" {
			d.engine.Clear(slot)
			continue
		}

		m, ok := wake.Find(models, ids[slot])
		if !ok {
			slog.Warn("unknown wake word", "slot", slot+1, "id", ids[slot])
			d.engine.Clear(slot)
			continue
		}
		if err := d.engine.Use(slot, m); err != nil {
			slog.Error("loading the selected wake word failed", "slot", slot+1, "id", m.ID, "err", err)
			d.engine.Clear(slot)
			continue
		}
		accepted = append(accepted, m.ID)
		loaded = append(loaded, slot)
	}

	// Only the slots that took a wake word are waited on. A selection that loaded nothing has nothing to
	// warm up, so the ring goes back to what it was showing rather than animating for a device that is
	// not going to hear anything.
	d.busy.waitFor(loaded)
	return accepted
}
