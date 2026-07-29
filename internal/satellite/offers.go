package satellite

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/wake"
)

// OnOffers is called with the models Home Assistant hosts in its own custom_wake_words directory,
// which it sends with every configuration request and is how a wake word reaches a device without
// anyone touching it.
//
// All of them are advertised as available. Whether any is worth downloading, and when, is not this
// side's business — the published collections run to hundreds of words.
func (s *Satellite) OnOffers(accept func([]wake.Offer)) {
	if s.kit.Voice == nil {
		return
	}

	s.kit.Voice.OnExternalWakeWords = func(offered []esphome.ExternalWakeWord) []esphome.WakeWord {
		out := make([]esphome.WakeWord, 0, len(offered))
		offers := make([]wake.Offer, 0, len(offered))

		// Home Assistant keys its wake word selects by phrase rather than by id, so two entries
		// saying "Glados" collapse to whichever came last. An offer that repeats a phrase this device
		// already has would take the key and, not being what is active, blank the select.
		taken := map[string]bool{}
		for _, w := range s.kit.Voice.AvailableWakeWords {
			taken[w.Phrase] = true
		}

		var shadowed int
		for _, e := range offered {
			if taken[e.Phrase] {
				shadowed++
				continue
			}
			taken[e.Phrase] = true

			offers = append(offers, wake.Offer{
				ID:        e.ID,
				Phrase:    e.Phrase,
				Languages: e.TrainedLanguages,
				Size:      e.Size,
				Hash:      e.Hash,
				URL:       e.URL,
			})
			out = append(out, esphome.WakeWord{
				ID: e.ID, Phrase: e.Phrase, TrainedLanguages: e.TrainedLanguages,
			})
		}

		accept(offers)
		slog.Info("wake words offered", "count", len(offered), "advertised", len(out),
			"shadowed", shadowed)
		return out
	}
}
