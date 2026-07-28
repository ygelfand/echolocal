// Package wake runs wake word detection over the microphone frames.
package wake

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ygelfand/echolocal/internal/alog"
	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/oww"
)

// Defaults for a model that arrives without a manifest, which most do.
const (
	WindowSize  = 5
	FeatureStep = 10
)

// DefaultCutoff applies when nothing supplies a threshold.
const DefaultCutoff = 0.85

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

// Engine feeds microphone frames to a detector.
type Engine struct {
	mu    sync.Mutex
	det   detector
	model Model

	// front is the openWakeWord front end, built on first use and kept: it holds the audio history
	// every wake word of that kind reads, so swapping the wake word must not disturb it.
	front *oww.Engine

	// Enabled and Cutoff are asked per frame, so changes in Home Assistant take effect at once.
	Enabled func() bool
	Cutoff  func() float64

	// OnDetect runs on a detection, off the audio path.
	OnDetect func()
}

// New loads a model. Thresholding happens in Run, against Cutoff.
func New(m Model) (*Engine, error) {
	e := &Engine{}
	det, err := e.build(m)
	if err != nil {
		return nil, err
	}
	e.det, e.model = det, m
	return e, nil
}

// build makes the detector for a model, bringing up the openWakeWord front end if this is the first
// wake word that needs it.
func (e *Engine) build(m Model) (detector, error) {
	if m.Kind != KindOpenWakeWord {
		return newMicro(m)
	}
	if e.front == nil {
		front, err := oww.New()
		if err != nil {
			return nil, err
		}
		e.front = front
	}
	return newOpenWakeWord(e.front, m)
}

// Model reports which wake word is loaded.
func (e *Engine) Model() Model {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.model
}

// Use swaps the loaded model, for when Home Assistant picks a different wake word. The detector is
// rebuilt, which is unavoidable: its weights and streaming state belong to the old model.
func (e *Engine) Use(m Model) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if m.ID == e.model.ID {
		return nil
	}
	det, err := e.build(m)
	if err != nil {
		return err
	}
	e.det.close()
	e.det, e.model = det, m

	slog.Info("wake word loaded", "phrase", m.Phrase, "id", m.ID, "engine", m.Kind)
	return nil
}

// feed scores one frame. The lock is held across the whole call rather than only while reading the
// detector: openWakeWord's front end is shared with whatever Use installs next, and it is not safe
// to swap a wake word out from under an inference.
func (e *Engine) feed(frame []int16) (float64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.det.feed(frame)
}

// Run consumes frames until ctx is cancelled.
func (e *Engine) Run(ctx context.Context, source *mic.Source) {
	frames, unlisten := source.Listen()
	defer unlisten()

	var (
		quiet      time.Time
		peak       float64
		peakAt     time.Time
		suppressed int

		// While holding, a detection has already fired and scoring continues only to find the
		// peak the utterance reached, so hits and misses are both reported as peaks.
		holding  bool
		holdEnds time.Time
		crossing float64
	)

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}
			if e.Enabled != nil && !e.Enabled() {
				continue
			}

			// Always feed the detector. It is a streaming model: a gap in its input is a gap in
			// its history, and the next utterance gets scored from a cold state. The refractory
			// below suppresses reporting, not audio.
			score, scored := e.feed(frame)
			if !scored {
				// openWakeWord produces a score every 80 ms, so most frames only advance it.
				continue
			}
			now := time.Now()

			cutoff := DefaultCutoff
			if e.Cutoff != nil {
				cutoff = e.Cutoff()
			}
			hit := score >= cutoff

			if now.Before(quiet) {
				suppressed++
				continue
			}
			if suppressed > 0 {
				slog.Debug("detections suppressed while settling", "frames", suppressed)
				suppressed = 0
			}

			if holding {
				if score > peak {
					peak = score
				}
				if now.Before(holdEnds) {
					continue
				}

				slog.Info("wake detected", "peak", peak, "crossing", crossing,
					"cutoff", cutoff, "dropped", source.Dropped())
				holding, peak = false, 0
				quiet = now.Add(Refractory)
				continue
			}

			if !hit {
				if score > peak {
					peak, peakAt = score, now
				}
				// The utterance peaked and fell away without firing.
				if peak >= NearMiss && now.Sub(peakAt) > NearMissSettle {
					slog.Info("wake near miss", "peak", peak,
						"cutoff", cutoff, "dropped", source.Dropped())
					peak = 0
				}
				continue
			}

			// Feedback fires now; the peak is only for the log, and the detector is reset when the
			// hold ends rather than here.
			holding, holdEnds = true, now.Add(Hold)
			crossing, peak = score, score

			if e.OnDetect != nil {
				go alog.Safely("wake detected", e.OnDetect)
			}
		}
	}
}
