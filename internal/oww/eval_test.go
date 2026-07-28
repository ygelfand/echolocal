package oww

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestEvalCollection scores synthesized speech against a directory of real wake word models. Each
// clip is scored by every model, so the same run shows both whether the right model fires and
// whether the others stay quiet. Set EVAL_MODELS and EVAL_AUDIO to run it.
func TestEvalCollection(t *testing.T) {
	modelDir, audioDir := os.Getenv("EVAL_MODELS"), os.Getenv("EVAL_AUDIO")
	if modelDir == "" || audioDir == "" {
		t.Skip("set EVAL_MODELS and EVAL_AUDIO")
	}

	clips, err := filepath.Glob(filepath.Join(audioDir, "*.wav"))
	if err != nil || len(clips) == 0 {
		t.Skipf("no clips in %s", audioDir)
	}
	sort.Strings(clips)

	models, err := filepath.Glob(filepath.Join(modelDir, "*.tflite"))
	if err != nil || len(models) == 0 {
		t.Skipf("no models in %s", modelDir)
	}

	// Only load models whose phrase one of the clips covers, plus a few others as distractors.
	loaded := map[string]string{}
	for _, m := range models {
		id := phraseOf(filepath.Base(m))
		for _, c := range clips {
			if phraseOf(filepath.Base(c)) == id {
				loaded[id] = m
			}
		}
	}
	if len(loaded) == 0 {
		t.Skip("no model matched a clip name")
	}

	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for id, path := range loaded {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Load(id, raw); err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	t.Logf("loaded %d models: %v", len(ids), ids)

	var hits, misses int
	for _, clip := range clips {
		want := phraseOf(filepath.Base(clip))
		pcm := readWAV(t, clip)

		// A fresh engine per clip: the pipeline carries history, and one clip must not prime the
		// next. Silence first, because scoring needs a full embedding window.
		fresh, err := New()
		if err != nil {
			t.Fatal(err)
		}
		overrideTransform(t, fresh)
		for _, id := range ids {
			raw, _ := os.ReadFile(loaded[id])
			if _, err := fresh.Load(id, raw); err != nil {
				t.Fatal(err)
			}
		}

		// Silence either side, so the phrase can sit in the middle of a scoring window and not
		// only ever at its trailing edge.
		peaks := map[string]float32{}
		pad := make([]int16, SampleRate*3/2)
		feed(t, fresh, pad, peaks)
		feed(t, fresh, pcm, peaks)
		feed(t, fresh, pad, peaks)

		var top string
		for id, v := range peaks {
			if top == "" || v > peaks[top] {
				top = id
			}
		}

		switch {
		case want == "negative":
			if peaks[top] > 0.5 {
				t.Errorf("negative clip scored %.3f on %s", peaks[top], top)
			}
			t.Logf("%-28s highest %-14s %.4f", filepath.Base(clip), top, peaks[top])
		case peaks[want] >= 0.5:
			hits++
			t.Logf("%-28s %-14s %.4f  (next %s %.4f)", filepath.Base(clip), want, peaks[want], top, peaks[top])
		default:
			misses++
			t.Logf("%-28s %-14s %.4f  MISS (highest %s %.4f)", filepath.Base(clip), want, peaks[want], top, peaks[top])
		}
	}
	t.Logf("%d detected, %d missed", hits, misses)
	if hits == 0 {
		t.Error("nothing was detected at all")
	}
}

func feed(t *testing.T, e *Engine, pcm []int16, peaks map[string]float32) {
	t.Helper()
	const frame = 320
	for off := 0; off < len(pcm); off += frame {
		scores, err := e.Process(pcm[off:min(off+frame, len(pcm))])
		if err != nil {
			t.Fatal(err)
		}
		for id, v := range scores {
			if v > peaks[id] {
				peaks[id] = v
			}
		}
	}
}

// phraseOf reduces a model or clip filename to the phrase it is about, so the two can be matched:
// "..._en_hey_Marvin.tflite" and "hey_marvin__Karen.wav" both reduce to "hey marvin". Clips name
// their voice after a double underscore, which a wake word never contains.
func phraseOf(name string) string {
	s := strings.TrimSuffix(strings.TrimSuffix(name, ".tflite"), ".wav")
	if i := strings.Index(s, "_en_"); i >= 0 {
		s = s[i+4:]
	}
	if i := strings.Index(s, "__"); i >= 0 {
		s = s[:i]
	}
	for _, suffix := range []string{"_v1", "_v2", "_v0.1"} {
		s = strings.TrimSuffix(s, suffix)
	}
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "_", " "), "-", " "))
}

func readWAV(t *testing.T, path string) []int16 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	total := uint64(len(raw))
	for off := uint64(12); off+8 <= total; {
		id := string(raw[off : off+4])
		size := uint64(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		off += 8
		end := min(off+size, total)
		if id == "data" {
			pcm := make([]int16, (end-off)/2)
			for i := range pcm {
				pcm[i] = int16(binary.LittleEndian.Uint16(raw[off+uint64(2*i):]))
			}
			return pcm
		}
		off = end + size%2
	}
	t.Fatalf("%s has no data chunk", path)
	return nil
}

// overrideTransform lets a run measure a different mel transform than the one in use, so the
// pipeline can be compared against the reference implementation's numbers.
func overrideTransform(t *testing.T, e *Engine) {
	t.Helper()
	if v := os.Getenv("OWW_MEL_SCALE"); v != "" {
		f, err := strconv.ParseFloat(v, 32)
		if err != nil {
			t.Fatal(err)
		}
		e.melScale = float32(f)
	}
	if v := os.Getenv("OWW_MEL_OFFSET"); v != "" {
		f, err := strconv.ParseFloat(v, 32)
		if err != nil {
			t.Fatal(err)
		}
		e.melOffset = float32(f)
	}
}
