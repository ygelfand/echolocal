package installer

import (
	"fmt"
	"path/filepath"

	"github.com/ygelfand/echolocal/internal/assets"
	"github.com/ygelfand/echolocal/internal/layout"
)

// installModels puts the wake words a device starts with in place, so one that has never met Home
// Assistant can still hear something. Without them the wake word entities have nothing to offer and
// read as unavailable until Home Assistant hosts a model of its own.
//
// Only what is missing is written, which makes this safe to re-run and means a model let go of by a
// cache purge comes back on the next install rather than being gone for good. A model somebody put
// there by hand is never overwritten.
func installModels(r *run) (string, bool, error) {
	models, err := assets.Models()
	if err != nil {
		return "", false, err
	}
	if len(models) == 0 {
		return "this build ships none", true, nil
	}

	if _, err := r.d.Shell("mkdir -p " + layout.ModelDir); err != nil {
		return "", false, err
	}

	var written, kept int
	for _, m := range models {
		path := filepath.Join(layout.ModelDir, m.ID+".tflite")

		have, err := r.d.Exists(path)
		if err != nil {
			return "", false, err
		}
		if have {
			kept++
			continue
		}

		// The manifest carries the phrase and the window the engine reads it with, so it is written
		// first: a model without one falls back to defaults that are only right by coincidence.
		if err := r.d.WriteFile(filepath.Join(layout.ModelDir, m.ID+".json"), m.Manifest, 0o644); err != nil {
			return "", false, err
		}
		if err := r.d.WriteFile(path, m.Model, 0o644); err != nil {
			return "", false, err
		}
		written++
	}

	if written == 0 {
		return fmt.Sprintf("%d already there", kept), true, nil
	}
	return fmt.Sprintf("%d written, %d already there", written, kept), false, nil
}
