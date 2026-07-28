package wake

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/zserge/microwakeword"

	"github.com/ygelfand/echolocal/internal/oww"
)

// backend runs the wake words of one Kind. It is the whole engine rather than one detector because
// openWakeWord's front end is shared: the mel and embedding chain has to consume each frame exactly
// once no matter how many wake words are listening to it, so scoring is per backend and the result
// is a score per wake word.
//
// Implementations are streaming. Every frame has to be fed, in order, or the next utterance is
// scored from a cold state.
type backend interface {
	// load makes a wake word scoreable, replacing one already loaded under the same id.
	load(m Model) error

	// unload drops one.
	unload(id string)

	// feed consumes one frame and reports a score per loaded wake word, and whether those scores
	// are new. A backend that scores less often than once per frame reports false in between, and
	// the scores it returns then are meaningless.
	feed(frame []int16) (map[string]float64, bool)

	close()
}

// newBackend builds the engine for a Kind.
func newBackend(k Kind) (backend, error) {
	if k == KindOpenWakeWord {
		front, err := oww.New()
		if err != nil {
			return nil, err
		}
		return &owwBackend{front: front, scores: map[string]float64{}}, nil
	}
	return &microBackend{dets: map[string]*microwakeword.Detector{}, scores: map[string]float64{}}, nil
}

// microBackend is microWakeWord: each wake word is a self-contained streaming model with its own
// feature front end, so they cost what they cost and every one of them sees every frame.
type microBackend struct {
	dets   map[string]*microwakeword.Detector
	scores map[string]float64
}

// neverFires is the cutoff handed to the detector so its own threshold never triggers. The
// threshold is ours: it comes from Home Assistant and changes while running, and rebuilding the
// detector to apply it would wipe the streaming state a model needs to score well.
const neverFires = 1.1

func (b *microBackend) load(m Model) error {
	cfg := m.Config
	cfg.ProbabilityCutoff = neverFires

	det, err := microwakeword.NewDetector(cfg)
	if err != nil {
		return fmt.Errorf("wake: loading %s: %w", m.Path, err)
	}
	b.dets[m.ID] = det
	return nil
}

func (b *microBackend) unload(id string) {
	delete(b.dets, id)
	delete(b.scores, id)
}

func (b *microBackend) feed(frame []int16) (map[string]float64, bool) {
	for id, det := range b.dets {
		det.ProcessAudio(frame)
		b.scores[id] = det.SlidingAverage()
	}
	return b.scores, true
}

func (b *microBackend) close() { clear(b.dets) }

// owwBackend is openWakeWord: one mel and embedding chain, and a small classifier per wake word
// reading the same window of embeddings. A second wake word costs a classifier, not a front end.
type owwBackend struct {
	front  *oww.Engine
	scores map[string]float64
}

func (b *owwBackend) load(m Model) error {
	model, err := os.ReadFile(m.Path)
	if err != nil {
		return fmt.Errorf("wake: reading %s: %w", m.Path, err)
	}
	_, err = b.front.Load(m.ID, model)
	return err
}

func (b *owwBackend) unload(id string) {
	b.front.Unload(id)
	delete(b.scores, id)
}

func (b *owwBackend) feed(frame []int16) (map[string]float64, bool) {
	scores, err := b.front.Process(frame)
	if err != nil {
		slog.Error("wake scoring failed", "err", err)
		return nil, false
	}
	if len(scores) == 0 {
		return nil, false
	}

	for id, v := range scores {
		b.scores[id] = float64(v)
	}
	return b.scores, true
}

func (b *owwBackend) close() {}
