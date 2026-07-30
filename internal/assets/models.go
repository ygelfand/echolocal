package assets

import (
	"embed"
	"io/fs"
)

// Wake words a device starts with, so one that has never met Home Assistant can still hear something.
// These are the three the reference satellite ships — okay_nabu, hey_jarvis and hey_mycroft, from
// esphome/micro-wake-word-models.
//
// Embedded unconditionally rather than staged like echod and the boot image: all three are under
// 200 KB, so every build carries them and there is no build tag to remember.
//
//go:embed models
var models embed.FS

// Model is one wake word as two files: the model itself, and the manifest beside it that says what the
// phrase is and how the engine should window it.
type Model struct {
	ID       string
	Model    []byte
	Manifest []byte
}

// Models is what a fresh device is given.
func Models() ([]Model, error) {
	entries, err := fs.ReadDir(models, "models")
	if err != nil {
		return nil, err
	}

	var out []Model
	for _, e := range entries {
		name := e.Name()
		if len(name) < 8 || name[len(name)-7:] != ".tflite" {
			continue
		}
		id := name[:len(name)-7]

		model, err := models.ReadFile("models/" + name)
		if err != nil {
			return nil, err
		}
		manifest, err := models.ReadFile("models/" + id + ".json")
		if err != nil {
			return nil, err
		}
		out = append(out, Model{ID: id, Model: model, Manifest: manifest})
	}
	return out, nil
}
