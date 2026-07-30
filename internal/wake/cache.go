package wake

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
)

// A model on disk is a copy of something Home Assistant serves, so the ones no slot is listening for
// are cache: deleting one costs a download the next time it is selected, and Home Assistant goes on
// offering it in the meantime.

// Cached is the models no slot is using, and the bytes they occupy.
func Cached(dir string, inUse []string) ([]Model, int64) {
	models, err := Installed(dir)
	if err != nil {
		return nil, 0
	}

	var stale []Model
	var bytes int64
	for _, m := range models {
		if slices.Contains(inUse, m.ID) {
			continue
		}
		stale = append(stale, m)
		bytes += size(dir, m)
	}
	return stale, bytes
}

// Purge deletes them, and reports how many went and how much that freed. A file that will not delete
// is logged and skipped: the rest of the cache is still worth clearing.
func Purge(dir string, inUse []string) (int, int64) {
	stale, _ := Cached(dir, inUse)

	var gone int
	var freed int64
	for _, m := range stale {
		was := size(dir, m)
		if err := remove(dir, m); err != nil {
			slog.Error("deleting a cached wake word failed", "id", m.ID, "err", err)
			continue
		}
		gone++
		freed += was
		slog.Info("cached wake word deleted", "id", m.ID, "phrase", m.Phrase, "bytes", was)
	}
	return gone, freed
}

// size is what a model occupies: the model itself and the manifest beside it.
func size(dir string, m Model) int64 {
	var total int64
	for _, path := range files(dir, m) {
		if fi, err := os.Stat(path); err == nil {
			total += fi.Size()
		}
	}
	return total
}

func remove(dir string, m Model) error {
	for _, path := range files(dir, m) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// files is everything on disk that belongs to a model: the manifest is named for the id, and the
// model is whatever the manifest named, which is usually but not always the same.
func files(dir string, m Model) []string {
	out := []string{m.Path, filepath.Join(dir, m.ID+".json")}
	if named := modelPath(dir, m.ID); named != m.Path {
		out = append(out, named)
	}
	return out
}
