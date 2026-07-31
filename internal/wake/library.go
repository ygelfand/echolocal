package wake

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/layout"
)

// Library is the models a device has and the ones Home Assistant is offering it.
//
// Everything offered can be advertised without being downloaded: the published collections run to
// hundreds of words, and a device does not need the ones nobody chose. A model arrives when it is
// selected, which is the one moment somebody is waiting for it.
//
// The two halves are kept apart because they change for different reasons. Ours changes when a
// selection downloads something or a purge deletes it. Theirs changes only when Home Assistant says
// so, and has to be remembered: the offer carries the URL and the selection that acts on it carries
// only the id, so they arrive in separate messages and this is where they meet.
type Library struct {
	dir string

	// ours is cached because reading it parses every model to work out which engine runs it.
	muOurs sync.Mutex
	ours   []Model

	muOffers sync.Mutex
	offers   map[string]esphome.ExternalWakeWord
}

// adoptTimeout bounds a download, since it happens while Home Assistant waits for a selection to be
// confirmed.
const adoptTimeout = 20 * time.Second

// There is one set of models on one disk, so there is one library. Everything that asks what the device
// can hear — the answer to Home Assistant, detection, the cache diagnostics — has to agree, and passing
// an instance between them is how they end up not agreeing.
var (
	once sync.Once
	lib  *Library
)

// Lib is the device's library, built on first use.
func Lib() *Library {
	once.Do(func() { lib = NewLibrary(layout.ModelDir) })
	return lib
}

// NewLibrary is for a library somewhere other than the device's own directory, which in practice means
// a test.
func NewLibrary(dir string) *Library {
	l := &Library{dir: dir, offers: map[string]esphome.ExternalWakeWord{}}
	l.Reload()
	return l
}

// Dir is where models are kept.
func (l *Library) Dir() string { return l.dir }

// Ours is what the device has.
func (l *Library) Ours() []Model {
	l.muOurs.Lock()
	defer l.muOurs.Unlock()
	return l.ours
}

// Reload re-reads the directory, and is called wherever what is on disk changes.
func (l *Library) Reload() {
	models, err := Installed(l.dir)
	if err != nil {
		slog.Error("listing wake words failed", "dir", l.dir, "err", err)
		return
	}

	l.muOurs.Lock()
	l.ours = models
	l.muOurs.Unlock()
}

// Offered records what Home Assistant is hosting, replacing what it offered before.
func (l *Library) Offered(offers []esphome.ExternalWakeWord) {
	next := make(map[string]esphome.ExternalWakeWord, len(offers))
	for _, o := range offers {
		next[o.ID] = o
	}

	l.muOffers.Lock()
	defer l.muOffers.Unlock()
	l.offers = next
}

// Advertise is everything the device can offer: what it has, plus what Home Assistant is hosting. It
// also reports how many offers were left out.
//
// A phrase appears once. Home Assistant keys its wake word selects by phrase rather than by id, so two
// entries saying "Glados" collapse to whichever came last, and if the survivor is not the one that is
// active the select shows nothing. Ours are laid down first, so a model already on disk keeps the phrase
// and the offer that repeats it is the one dropped — the device prefers what it can load without a
// download, whichever engine that turns out to be.
//
// Being left out only affects what Home Assistant displays. Every offer keeps its URL in the map above,
// because Home Assistant selects by id and can ask for one it was never shown.
func (l *Library) Advertise() ([]esphome.WakeWord, int) {
	ours := l.Ours()

	words := make([]esphome.WakeWord, 0, len(ours))
	taken := make(map[string]bool, len(ours))
	for _, m := range ours {
		words = append(words, esphome.WakeWord{ID: m.ID, Phrase: m.Phrase, TrainedLanguages: m.Languages})
		taken[m.Phrase] = true
	}

	var shadowed int
	for _, o := range l.sorted() {
		if taken[o.Phrase] {
			shadowed++
			continue
		}
		taken[o.Phrase] = true
		words = append(words, esphome.WakeWord{ID: o.ID, Phrase: o.Phrase, TrainedLanguages: o.TrainedLanguages})
	}
	return words, shadowed
}

// sorted is what Home Assistant offered, in id order, so which of two entries sharing a phrase gets
// advertised does not change from one request to the next.
func (l *Library) sorted() []esphome.ExternalWakeWord {
	l.muOffers.Lock()
	defer l.muOffers.Unlock()

	out := make([]esphome.ExternalWakeWord, 0, len(l.offers))
	for _, o := range l.offers {
		out = append(out, o)
	}
	slices.SortFunc(out, func(a, b esphome.ExternalWakeWord) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// Purge deletes the models no slot is listening for and reports how many went and what that freed.
func (l *Library) Purge(inUse []string) (int, int64) {
	gone, freed := Purge(l.dir, inUse)
	if gone > 0 {
		l.Reload()
	}
	return gone, freed
}

// Ensure downloads any of these that are offered and not already here, and reports everything the
// device has afterwards, which is what a selection is resolved against.
func (l *Library) Ensure(ids []string) []Model {
	var fetched bool
	for _, id := range ids {
		if id == "" {
			continue
		}
		o := l.offer(id)
		if o.URL == "" || Have(l.dir, o) {
			continue
		}
		l.fetch(o)
		fetched = true
	}

	if fetched {
		l.Reload()
	}
	return l.Ours()
}

func (l *Library) offer(id string) esphome.ExternalWakeWord {
	l.muOffers.Lock()
	defer l.muOffers.Unlock()
	return l.offers[id]
}

func (l *Library) fetch(o esphome.ExternalWakeWord) {
	ctx, cancel := context.WithTimeout(context.Background(), adoptTimeout)
	defer cancel()

	m, err := Adopt(ctx, l.dir, o)
	if err != nil {
		slog.Error("adopting a wake word failed", "id", o.ID, "err", err)
		return
	}
	slog.Info("wake word adopted", "id", m.ID, "phrase", m.Phrase, "engine", m.Kind)
}
