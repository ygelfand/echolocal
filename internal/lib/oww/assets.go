// Package oww runs openWakeWord models: a shared mel front end and speech embedding feeding a
// per-wake-word classifier.
package oww

import _ "embed"

// The mel and embedding models are the same for every openWakeWord wake word, and every
// classifier is trained against these exact two, so they ship with the engine rather than
// alongside the models a user installs.
var (
	//go:embed assets/melspectrogram.tflite
	melModel []byte

	//go:embed assets/embedding_model.tflite
	embedModel []byte
)
