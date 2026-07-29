package boot

import (
	"log/slog"

	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/satellite"
	"github.com/ygelfand/echolocal/internal/service"
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/wake"
)

// addWake supervises detection and joins it to Home Assistant's wake word slots.
//
// The engine is the service. This is the part that knows what the user chose, which the engine does
// not: which words go in which slot, and what to tell Home Assistant about the ones that would not
// load.
func addWake(group *service.Group, sat *satellite.Satellite, source *mic.Source) {
	library := wake.NewLibrary(layout.ModelDir)

	models, err := wake.Installed(library.Dir())
	if err != nil {
		slog.Warn("no wake word models", "dir", library.Dir(), "err", err)
		return
	}

	backend := settings.Get().Wake.BackendOr(settings.DefaultBackend)
	engine := wake.New(backend, satellite.WakeSlots, source)
	engine.Threshold = sat.WakeThreshold
	engine.OnDetect = sat.WakeDetected

	// load puts one wake word in each slot and reports the ids that came up. Whatever the engine
	// refuses is left out, so Home Assistant reverts that slot rather than showing a wake word the
	// device is not listening for.
	load := func(ids []string) []string {
		// A selection may name a model Home Assistant is offering but this device has never had, so the
		// library is asked rather than the list captured at boot: this is where a new word arrives.
		models := library.Ensure(ids)

		var accepted []string
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
		}
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

	// Changing the engine rebuilds it and reloads every slot from what that engine was last used with.
	sat.OnWakeBackend(func(b settings.WakeBackend) []string {
		if err := engine.SetBackend(b); err != nil {
			slog.Error("changing the wake engine failed", "backend", b, "err", err)
			return nil
		}

		saved := settings.Get().Wake
		ids := make([]string, satellite.WakeSlots)
		for slot := range ids {
			ids[slot] = saved.WordID(slot)
		}
		return load(ids)
	})

	group.Add(engine, forever())
	slog.Info("wake words installed", "count", len(models),
		"backend", backend, "runnable", len(wake.OfKind(models, backend)))
}
