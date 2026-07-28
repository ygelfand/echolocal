package wake

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zserge/microwakeword"

	"github.com/ygelfand/echolocal/internal/oww"
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/tflite"
)

// Model is an installed wake word: the file to run and what to call it in Home Assistant.
type Model struct {
	// ID is the file's base name. Home Assistant selects by it.
	ID string

	// Phrase is what the user says, shown in Home Assistant's picker.
	Phrase string

	// Languages the model was trained on, as BCP-47 tags.
	Languages []string

	// Kind is which engine runs it. Both are plain .tflite files that arrive the same way, so the
	// model itself has to say, and it does: an openWakeWord wake word classifies a window of speech
	// embeddings and takes them as its input, while a microWakeWord model is self-contained and
	// consumes audio features.
	Kind settings.WakeBackend

	Path   string
	Config microwakeword.Config
}

// manifest is the JSON that ships beside a model, as Home Assistant serves it from
// custom_wake_words. For a model Home Assistant offers us, the id, phrase and languages come from
// the offer itself and are written here when it is adopted; this is also what lets a model copied
// on by hand carry its own label.
type manifest struct {
	WakeWord         string   `json:"wake_word"`
	Model            string   `json:"model"`
	TrainedLanguages []string `json:"trained_languages"`

	Micro struct {
		SlidingWindowSize int `json:"sliding_window_size"`
		FeatureStepSize   int `json:"feature_step_size"`
	} `json:"micro"`
}

// Installed lists the models in dir, one per .tflite, reading a sidecar JSON of the same name when
// there is one. A model with no manifest still works: the defaults are the ones every published
// microWakeWord model uses, and the phrase comes from the file name.
func Installed(dir string) ([]Model, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var out []Model
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".tflite" {
			continue
		}

		id := strings.TrimSuffix(e.Name(), ".tflite")
		out = append(out, load(dir, id))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("wake: no models in %s", dir)
	}
	return out, nil
}

// kindOf asks the model which engine runs it. An openWakeWord classifier takes a window of
// embeddings, [1, frames, 96]; anything else is treated as microWakeWord, which is the safe default
// because that engine reports a load failure while a wrong guess the other way would score noise.
func kindOf(path string) settings.WakeBackend {
	raw, err := os.ReadFile(path)
	if err != nil {
		return settings.BackendMicroWakeWord
	}
	m, err := tflite.Parse(raw)
	if err != nil {
		return settings.BackendMicroWakeWord
	}

	g := m.Subgraphs[0]
	if len(g.Inputs) == 0 {
		return settings.BackendMicroWakeWord
	}
	shape := g.Tensors[g.Inputs[0]].Shape
	if len(shape) == 3 && shape[2] == oww.EmbeddingDims {
		return settings.BackendOpenWakeWord
	}
	return settings.BackendMicroWakeWord
}

func load(dir, id string) Model {
	path := filepath.Join(dir, id+".tflite")
	m := Model{
		ID:        id,
		Phrase:    phrase(id),
		Languages: []string{"en"},
		Kind:      kindOf(path),
		Path:      path,
		Config: microwakeword.Config{
			SlidingWindowSize: WindowSize,
			FeaturesStepMs:    FeatureStep,
		},
	}
	m.Config.ModelPath = m.Path

	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return m
	}

	var man manifest
	if err := json.Unmarshal(data, &man); err != nil {
		return m
	}

	if man.WakeWord != "" {
		m.Phrase = man.WakeWord
	}
	if len(man.TrainedLanguages) > 0 {
		m.Languages = man.TrainedLanguages
	}
	if man.Model != "" {
		m.Path = filepath.Join(dir, man.Model)
		m.Config.ModelPath = m.Path
	}

	// Window and step describe the model. probability_cutoff is deliberately ignored: the
	// threshold is the user's, set from Home Assistant.
	if man.Micro.SlidingWindowSize > 0 {
		m.Config.SlidingWindowSize = man.Micro.SlidingWindowSize
	}
	if man.Micro.FeatureStepSize > 0 {
		m.Config.FeaturesStepMs = man.Micro.FeatureStepSize
	}
	return m
}

// phrase turns hey_jarvis into "Hey Jarvis", for a model that arrived without a manifest.
func phrase(id string) string {
	words := strings.FieldsFunc(id, func(r rune) bool { return r == '_' || r == '-' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// Pick returns the model with the given id, or the first installed one when the id is empty or no
// longer present — a stale selection should not leave the device deaf.
func Pick(models []Model, id string) Model {
	for _, m := range models {
		if m.ID == id {
			return m
		}
	}
	return models[0]
}

// Find returns the model with the given id, and whether there is one. Unlike Pick it does not
// substitute: a slot the user set to nothing has to stay at nothing.
func Find(models []Model, id string) (Model, bool) {
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// OfKind is the models one backend can run.
func OfKind(models []Model, k settings.WakeBackend) []Model {
	var out []Model
	for _, m := range models {
		if m.Kind == k {
			out = append(out, m)
		}
	}
	return out
}

// Backends lists the ones with something installed to run, openWakeWord first because it is the one
// wake words are published for.
func Backends(models []Model) []settings.WakeBackend {
	var out []settings.WakeBackend
	for _, k := range []settings.WakeBackend{settings.BackendOpenWakeWord, settings.BackendMicroWakeWord} {
		if len(OfKind(models, k)) > 0 {
			out = append(out, k)
		}
	}
	return out
}
