package wake

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Library is the models a device has and the ones Home Assistant is offering it.
//
// Everything offered can be advertised without being downloaded: the published collections run to
// hundreds of words, and a device does not need the ones nobody chose. A model arrives when it is
// selected, which is the one moment somebody is waiting for it.
type Library struct {
	dir string

	mu     sync.Mutex
	offers map[string]Offer
}

// adoptTimeout bounds a download, since it happens while Home Assistant waits for a selection to be
// confirmed.
const adoptTimeout = 20 * time.Second

func NewLibrary(dir string) *Library {
	return &Library{dir: dir, offers: map[string]Offer{}}
}

// Dir is where models are kept.
func (l *Library) Dir() string { return l.dir }

// Offered records what Home Assistant is hosting, replacing what it offered before.
func (l *Library) Offered(offers []Offer) {
	next := make(map[string]Offer, len(offers))
	for _, o := range offers {
		next[o.ID] = o
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.offers = next
}

// Ensure downloads any of these that are offered and not already here, and reports everything
// installed afterwards, which is what a selection is resolved against.
func (l *Library) Ensure(ids []string) []Model {
	for _, id := range ids {
		if id == "" {
			continue
		}
		o := l.offer(id)
		if o.URL == "" || Have(l.dir, o) {
			continue
		}
		l.fetch(o)
	}

	models, err := Installed(l.dir)
	if err != nil {
		slog.Error("listing wake words failed", "dir", l.dir, "err", err)
		return nil
	}
	return models
}

func (l *Library) offer(id string) Offer {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.offers[id]
}

func (l *Library) fetch(o Offer) {
	ctx, cancel := context.WithTimeout(context.Background(), adoptTimeout)
	defer cancel()

	m, err := Adopt(ctx, l.dir, o)
	if err != nil {
		slog.Error("adopting a wake word failed", "id", o.ID, "err", err)
		return
	}
	slog.Info("wake word adopted", "id", m.ID, "phrase", m.Phrase, "engine", m.Kind)
}
