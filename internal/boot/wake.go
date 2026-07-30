package boot

import (
	"log/slog"

	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/satellite"
	"github.com/ygelfand/echolocal/internal/service"
	"github.com/ygelfand/echolocal/internal/wake"
)

// addWake supervises detection and joins it to Home Assistant's wake word slots.
//
// The engine is the service. This is the part that knows what the user chose, which the engine does
// not: which words go in which slot, and what to tell Home Assistant about the ones that would not
// load.
func addWake(group *service.Group, sat *satellite.Satellite, source *mic.Source, leds *led.Driver) {
	library := wake.NewLibrary(layout.ModelDir)

	// A device with no models still wires everything up. What Home Assistant offers arrives on the
	// configuration request, and dropping that handler is how a device ends up unable to adopt its
	// first wake word: nothing local, so nothing offered, so nothing to select.
	models, err := wake.Installed(library.Dir())
	if err != nil {
		slog.Warn("listing wake word models failed", "dir", library.Dir(), "err", err)
	}

	engine := wake.New(satellite.WakeSlots, source)
	engine.Threshold = sat.WakeThreshold
	engine.OnDetect = sat.WakeDetected

	busy := newWakeBusy(leds.Busy(), engine.Ready)
	engine.OnReady = busy.scored

	// load puts one wake word in each slot and reports the ids that came up. Whatever the engine
	// refuses is left out, so Home Assistant reverts that slot rather than showing a wake word the
	// device is not listening for.
	load := func(ids []string) []string {
		busy.begin()

		// A selection may name a model Home Assistant is offering but this device has never had, so the
		// library is asked rather than the list captured at boot: this is where a new word arrives.
		models := library.Ensure(ids)

		var accepted []string
		var loaded []int
		for slot := range satellite.WakeSlots {
			if slot >= len(ids) || ids[slot] == "" {
				engine.Clear(slot)
				continue
			}

			m, ok := wake.Find(models, ids[slot])
			if !ok {
				slog.Warn("unknown wake word", "slot", slot+1, "id", ids[slot])
				engine.Clear(slot)
				continue
			}
			if err := engine.Use(slot, m); err != nil {
				slog.Error("loading the selected wake word failed", "slot", slot+1, "id", m.ID, "err", err)
				engine.Clear(slot)
				continue
			}
			accepted = append(accepted, m.ID)
			loaded = append(loaded, slot)
		}

		// Only the slots that took a wake word are waited on. A selection that loaded nothing has nothing
		// to warm up, so the ring goes back to what it was showing rather than animating for a device that
		// is not going to hear anything.
		busy.waitFor(loaded)
		return accepted
	}

	// The engine loads on every start, including a restart. Home Assistant only pushes a selection
	// when the user changes one, so an engine that came back empty would leave the device deaf while
	// it went on advertising wake words it was not listening for.
	engine.Load = func() error {
		sat.SetActiveWakeWords(load(sat.ActiveWakeWords()))
		return nil
	}

	sat.OnWakeWord(load)
	sat.OnOffers(library.Offered)

	group.Add(engine, forever())
	slog.Info("wake words installed", "count", len(models),
		"openwakeword", len(wake.OfKind(models, wake.KindOpenWakeWord)),
		"microwakeword", len(wake.OfKind(models, wake.KindMicroWakeWord)))
}
