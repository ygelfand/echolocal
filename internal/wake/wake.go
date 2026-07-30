// Package wake runs wake word detection over the microphone frames.
package wake

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ygelfand/echolocal/internal/alog"
	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/settings"
)

// Defaults for a model that arrives without a manifest, which most do.
const (
	WindowSize  = 5
	FeatureStep = 10
)

// Hold is how long scoring continues after a detection fires, to record the peak the utterance
// reached. Feedback happens at once; this only decides what gets logged.
const Hold = 300 * time.Millisecond

// Refractory is how long detection pauses after a hit, so one utterance reports once rather than
// once per frame. Keep it short: anything longer swallows a genuine second attempt, which looks
// exactly like a failure to detect.
const Refractory = 800 * time.Millisecond

// NearMiss is the peak score worth reporting for an utterance that never fired, and NearMissSettle
// how long after that peak to wait before deciding it is not going to. The sliding average ramps
// through the low scores on its way to a real detection, so reporting as soon as the threshold is
// crossed reports successes, not failures.
const (
	NearMiss       = 0.4
	NearMissSettle = 700 * time.Millisecond
)

// Engine feeds microphone frames to the wake words of one backend.
//
// Slots are Home Assistant's: it offers a fixed number of wake word choices, each paired with its
// own pipeline, and a detection has to say which one fired so the right pipeline runs. A slot with
// no model is off, and all slots off is detection off — there is nothing else to switch.
type Engine struct {
	source *mic.Source

	mu sync.Mutex

	// backends is one engine per kind of model in use, built when a slot first wants it and dropped
	// when the last slot using it goes. Both can be up at once, a slot each, and each costs a front
	// end per frame — so which are running follows what is loaded rather than a setting.
	backends map[Kind]backend
	slots    []slot

	// Threshold is asked for every score, so a change in Home Assistant takes effect at once. It is
	// per slot because the models disagree on scale.
	Threshold func(slot int) float64

	// OnDetect runs on a detection, off the audio path.
	OnDetect func(slot int)

	// Load puts the wake words in once the engine is up. It runs on every start, including a restart,
	// because an engine that came back empty would leave the device deaf while claiming otherwise.
	// Which words those are depends on what the user last chose, which is not this package's business.
	Load func() error
}

// slot is one wake word and how its scores are being read. The tracking is per slot: two wake words
// listening to the same audio peak at different moments, and one firing must not silence the other.
type slot struct {
	model  Model
	loaded bool

	quiet      time.Time
	peak       float64
	peakAt     time.Time
	suppressed int

	// While holding, a detection has already fired and scoring continues only to find the peak the
	// utterance reached, so hits and misses are both reported as peaks.
	holding  bool
	holdEnds time.Time
	crossing float64
}

// New describes an engine. Nothing is built until Start.
func New(slots int, source *mic.Source) *Engine {
	return &Engine{backends: map[Kind]backend{}, slots: make([]slot, slots), source: source}
}

func (e *Engine) Name() string { return "wake" }

// Start empties the engine and loads the wake words. A restart comes back through here, so a detector
// that broke is rebuilt from scratch rather than resumed from whatever state it died in.
func (e *Engine) Start(context.Context) error {
	e.mu.Lock()
	e.reset()
	e.mu.Unlock()

	if e.Load == nil {
		return nil
	}
	return e.Load()
}

// Close drops the engines and everything loaded in them.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.reset()
	return nil
}

// reset closes every engine and empties every slot. Held with mu.
func (e *Engine) reset() {
	for k, b := range e.backends {
		b.close()
		delete(e.backends, k)
	}
	for i := range e.slots {
		e.slots[i] = slot{}
	}
}

// build makes an engine of one kind. A test replaces it with one that needs no model files.
var build = newBackend

// engineFor is the backend that runs a kind of model, built the first time one is wanted.
func (e *Engine) engineFor(k Kind) (backend, error) {
	if b, ok := e.backends[k]; ok {
		return b, nil
	}
	b, err := build(k)
	if err != nil {
		return nil, err
	}
	e.backends[k] = b
	return b, nil
}

// Use loads a wake word into a slot, in whichever engine runs that model.
func (e *Engine) Use(n int, m Model) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if n < 0 || n >= len(e.slots) {
		return fmt.Errorf("wake: slot %d out of range", n)
	}
	if s := e.slots[n]; s.loaded && s.model.ID == m.ID {
		return nil
	}

	b, err := e.engineFor(m.Kind)
	if err != nil {
		return err
	}
	if err := b.load(m); err != nil {
		e.drop(slot{model: m, loaded: true})
		return err
	}

	old := e.slots[n]
	e.slots[n] = slot{model: m, loaded: true}
	e.drop(old)

	slog.Info("wake word loaded", "slot", n+1, "phrase", m.Phrase, "id", m.ID, "engine", m.Kind)
	return nil
}

// Clear switches a slot off.
func (e *Engine) Clear(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if n < 0 || n >= len(e.slots) {
		return
	}
	if e.slots[n].loaded {
		slog.Info("wake word cleared", "slot", n+1, "id", e.slots[n].model.ID)
	}

	old := e.slots[n]
	e.slots[n] = slot{}
	e.drop(old)
}

// drop lets go of what a slot was using: the model, unless another slot still wants it, and then the
// engine itself, unless another slot still needs that. An engine with nothing loaded would go on
// costing a front end for every frame. Called with the slot table already updated, so what it asks
// about the other slots is the truth. Held with mu.
func (e *Engine) drop(was slot) {
	if !was.loaded {
		return
	}
	b := e.backends[was.model.Kind]
	if b == nil {
		return
	}

	if !e.uses(was.model.ID) {
		b.unload(was.model.ID)
	}
	if !e.runs(was.model.Kind) {
		b.close()
		delete(e.backends, was.model.Kind)
	}
}

// uses reports whether a slot is listening for a model. Held with mu.
func (e *Engine) uses(id string) bool {
	for _, s := range e.slots {
		if s.loaded && s.model.ID == id {
			return true
		}
	}
	return false
}

// runs reports whether a slot needs an engine. Held with mu.
func (e *Engine) runs(k Kind) bool {
	for _, s := range e.slots {
		if s.loaded && s.model.Kind == k {
			return true
		}
	}
	return false
}

// Loaded reports the wake word in a slot, and whether the slot is on.
func (e *Engine) Loaded(n int) (Model, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if n < 0 || n >= len(e.slots) {
		return Model{}, false
	}
	return e.slots[n].model, e.slots[n].loaded
}

// Listening reports whether any slot has a wake word.
func (e *Engine) Listening() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, s := range e.slots {
		if s.loaded {
			return true
		}
	}
	return false
}

// Run scores frames until ctx is cancelled. The microphones going away is reported rather than
// returned quietly: detection stopping is the device going deaf, which should not be silent.
func (e *Engine) Run(ctx context.Context) error {
	frames, unlisten := e.source.Listen()
	defer unlisten()

	for {
		select {
		case <-ctx.Done():
			return nil
		case frame, ok := <-frames:
			if !ok {
				return errors.New("wake: the microphones stopped")
			}
			e.score(frame, e.source)
		}
	}
}

// score feeds one frame and acts on what came back. The lock is held across the whole of it: an
// engine is shared with whatever Use installs next, and it is not safe to swap a wake word out from
// under an inference.
func (e *Engine) score(frame []int16, source *mic.Source) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Always feed. These are streaming models: a gap in the input is a gap in their history, and the
	// next utterance gets scored from a cold state. The refractory below suppresses reporting, not
	// audio. An engine only exists while a slot needs it, so nothing loaded feeds nothing.
	// Only what came back fresh is kept, so a slot whose engine has not scored this frame finds
	// nothing rather than reading a stale number. openWakeWord scores every 80 ms, so for it most
	// frames only advance the front end.
	heard := make(map[Kind]map[string]float64, len(e.backends))
	for k, b := range e.backends {
		if scores, fresh := b.feed(frame); fresh {
			heard[k] = scores
		}
	}

	now := time.Now()
	for i := range e.slots {
		s := &e.slots[i]
		if !s.loaded {
			continue
		}
		score, ok := heard[s.model.Kind][s.model.ID]
		if !ok {
			continue
		}
		e.judge(i, s, score, now, source)
	}
}

// judge turns one slot's score into a detection, a near miss, or nothing. Held with mu.
func (e *Engine) judge(n int, s *slot, score float64, now time.Time, source *mic.Source) {
	cutoff := settings.DefaultThreshold
	if e.Threshold != nil {
		cutoff = e.Threshold(n)
	}

	if now.Before(s.quiet) {
		s.suppressed++
		return
	}
	if s.suppressed > 0 {
		slog.Debug("detections suppressed while settling", "slot", n+1, "frames", s.suppressed)
		s.suppressed = 0
	}

	if s.holding {
		if score > s.peak {
			s.peak = score
		}
		if now.Before(s.holdEnds) {
			return
		}

		slog.Info("wake detected", "slot", n+1, "id", s.model.ID, "peak", s.peak,
			"crossing", s.crossing, "cutoff", cutoff, "dropped", source.Dropped())
		s.holding, s.peak = false, 0
		s.quiet = now.Add(Refractory)
		return
	}

	if score < cutoff {
		if score > s.peak {
			s.peak, s.peakAt = score, now
		}
		// The utterance peaked and fell away without firing.
		if s.peak >= NearMiss && now.Sub(s.peakAt) > NearMissSettle {
			slog.Info("wake near miss", "slot", n+1, "id", s.model.ID, "peak", s.peak,
				"cutoff", cutoff, "dropped", source.Dropped())
			s.peak = 0
		}
		return
	}

	// Feedback fires now; the peak is only for the log, and the slot settles when the hold ends.
	s.holding, s.holdEnds = true, now.Add(Hold)
	s.crossing, s.peak = score, score

	if e.OnDetect != nil {
		go alog.Safely("wake detected", func() { e.OnDetect(n) })
	}
}
