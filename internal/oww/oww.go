package oww

import (
	"fmt"

	"github.com/ygelfand/echolocal/internal/tflite"
)

const (
	// SampleRate is the only rate openWakeWord models are trained for.
	SampleRate = 16000

	// Step is the audio a single pipeline step consumes, 80 ms.
	Step = 1280

	// melLookback is fed to the mel model on top of the step so that the frames either side of a
	// step boundary come out the same as they would from one continuous pass.
	melLookback = 3 * 160

	melBins = 32

	// embedFrames is the mel window one embedding sees, and embedStride is how far the window
	// moves per step. The stride is Step/160, so exactly one embedding comes out per step.
	embedFrames = 76
	embedStride = 8

	embedDims = 96
)

// EmbeddingDims is the width of one speech embedding, which is also what identifies a classifier as
// an openWakeWord one: its input is a window of these.
const EmbeddingDims = embedDims

// Engine is the shared front end: audio in, speech embeddings out, with any number of wake word
// classifiers reading the embeddings. It is not safe for concurrent use.
type Engine struct {
	mel *tflite.Interpreter

	// embed runs incrementally: a step feeds it the 8 new mel frames, not the whole window.
	embed *tflite.Stream

	// raw holds the lookback the mel model needs plus whatever has arrived since the last step.
	raw []int16

	// mels is scratch for one step's frames; feats is a flat ring of embeddings, oldest first.
	mels  []float32
	feats []float32

	classifiers []*Classifier
	scores      map[string]float32
	maxFrames   int

	// The mel transform, as fields so a test can measure the pipeline against alternatives.
	melScale  float32
	melOffset float32
}

// The mel model reports on a scale the embedding model was not trained on, and openWakeWord
// bridges the two with this transform. Samples reach the model at int16 magnitudes, undivided.
const (
	melScale  = 0.1
	melOffset = 2
)

// New builds the front end from the embedded mel and embedding models.
func New() (*Engine, error) {
	parsed, err := tflite.Parse(melModel)
	if err != nil {
		return nil, fmt.Errorf("oww: mel model: %w", err)
	}
	mel, err := tflite.New(parsed)
	if err != nil {
		return nil, fmt.Errorf("oww: mel model: %w", err)
	}

	parsed, err = tflite.Parse(embedModel)
	if err != nil {
		return nil, fmt.Errorf("oww: embedding model: %w", err)
	}
	embed, err := tflite.NewStream(parsed, []int{1, embedFrames, melBins, 1})
	if err != nil {
		return nil, fmt.Errorf("oww: embedding model: %w", err)
	}

	e := &Engine{
		mel:   mel,
		embed: embed,
		// Empty, not primed with lookback: the first window has to start at the first real sample
		// so that frames land on multiples of the hop from the start of the audio, which is the
		// grid the models were trained on. Priming it with silence shifts every frame by
		// melLookback samples, and that shift is not a whole number of embedding strides.
		raw:       make([]int16, 0, melLookback+Step),
		mels:      make([]float32, 0, embedStride*melBins),
		scores:    map[string]float32{},
		melScale:  melScale,
		melOffset: melOffset,
	}

	return e, nil
}

// Classifier is one wake word: a model over the embedding stream.
type Classifier struct {
	ID string

	in     *tflite.Interpreter
	frames int
}

// Load adds a wake word classifier. The number of embeddings it reads comes from the model.
func (e *Engine) Load(id string, model []byte) (*Classifier, error) {
	m, err := tflite.Parse(model)
	if err != nil {
		return nil, fmt.Errorf("oww: %s: %w", id, err)
	}
	in, err := tflite.New(m)
	if err != nil {
		return nil, fmt.Errorf("oww: %s: %w", id, err)
	}

	shape := in.Input(0).Shape
	if len(shape) != 3 || shape[2] != embedDims {
		return nil, fmt.Errorf("oww: %s: input shape %v is not a %d-wide embedding window", id, shape, embedDims)
	}

	c := &Classifier{ID: id, in: in, frames: shape[1]}
	e.classifiers = append(e.classifiers, c)
	if c.frames > e.maxFrames {
		e.maxFrames = c.frames
	}
	return c, nil
}

// Unload removes a wake word.
func (e *Engine) Unload(id string) {
	kept := e.classifiers[:0]
	e.maxFrames = 0
	for _, c := range e.classifiers {
		if c.ID == id {
			continue
		}
		kept = append(kept, c)
		if c.frames > e.maxFrames {
			e.maxFrames = c.frames
		}
	}
	e.classifiers = kept
	delete(e.scores, id)
}

// Process consumes audio and returns the highest score each wake word reached. Audio arrives in
// frames smaller than a step, so a call usually completes no step and returns nothing; when a
// call spans several steps the peak is what matters, not the last one.
func (e *Engine) Process(pcm []int16) (map[string]float32, error) {
	e.raw = append(e.raw, pcm...)
	clear(e.scores)
	stepped := false

	for len(e.raw) >= melLookback+Step {
		if err := e.step(e.raw[:melLookback+Step]); err != nil {
			return nil, err
		}
		// Keep the tail as the next step's lookback.
		e.raw = append(e.raw[:0], e.raw[Step:]...)
		stepped = true

		if len(e.feats)/embedDims < e.maxFrames {
			continue
		}
		for _, c := range e.classifiers {
			score, err := c.score(e.feats)
			if err != nil {
				return nil, err
			}
			if score > e.scores[c.ID] {
				e.scores[c.ID] = score
			}
		}
	}

	if !stepped || len(e.scores) == 0 {
		return nil, nil
	}
	return e.scores, nil
}

// step turns one window of audio into mel frames and one embedding.
func (e *Engine) step(window []int16) error {
	e.mel.ResizeInput(0, []int{1, len(window)})
	in := e.mel.Input(0)
	for i, s := range window {
		in.F32[i] = float32(s)
	}
	if err := e.mel.Invoke(); err != nil {
		return fmt.Errorf("oww: mel: %w", err)
	}

	out := e.mel.Output(0)
	frames := out.Count() / melBins
	if frames != embedStride {
		return fmt.Errorf("oww: mel produced %d frames for a step, want %d", frames, embedStride)
	}
	// openWakeWord scales the mel output to match what the embedding model was trained on.
	e.mels = e.mels[:0]
	for _, v := range out.F32 {
		e.mels = append(e.mels, v*e.melScale+e.melOffset)
	}

	emb, err := e.embed.Write(e.mels)
	if err != nil {
		return err
	}
	// Nothing comes out until the first full window of mel frames has arrived.
	if len(emb) == 0 {
		return nil
	}
	if len(emb) != embedDims {
		return fmt.Errorf("oww: embedding produced %d values, want %d", len(emb), embedDims)
	}

	e.feats = append(e.feats, emb...)
	if keep := e.maxFrames * embedDims; keep > 0 && len(e.feats) > keep {
		e.feats = append(e.feats[:0], e.feats[len(e.feats)-keep:]...)
	}
	return nil
}

func (c *Classifier) score(feats []float32) (float32, error) {
	c.in.ResizeInput(0, []int{1, c.frames, embedDims})
	copy(c.in.Input(0).F32, feats[len(feats)-c.frames*embedDims:])

	if err := c.in.Invoke(); err != nil {
		return 0, fmt.Errorf("oww: %s: %w", c.ID, err)
	}
	out := c.in.Output(0)
	if out.Count() == 0 {
		return 0, fmt.Errorf("oww: %s produced no output", c.ID)
	}
	return out.F32[0], nil
}
