package oww

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestScoreOne scores one clip with one model, both given explicitly, so a number can be compared
// against another implementation with no model selection in the way. ONE_NOPAD feeds the clip as
// it is, for clips that already carry their own silence; ONE_SEQ prints every score.
func TestScoreOne(t *testing.T) {
	model, clip := os.Getenv("ONE_MODEL"), os.Getenv("ONE_CLIP")
	if model == "" || clip == "" {
		t.Skip("set ONE_MODEL and ONE_CLIP")
	}
	raw, err := os.ReadFile(model)
	if err != nil {
		t.Fatal(err)
	}
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	overrideTransform(t, e)
	if _, err := e.Load("m", raw); err != nil {
		t.Fatal(err)
	}

	pcm := readWAV(t, clip)
	if os.Getenv("ONE_NOPAD") == "" {
		pad := make([]int16, SampleRate*3/2)
		pcm = append(append(append([]int16{}, pad...), pcm...), pad...)
	}

	var seq []string
	var peak float32
	const frame = 320
	for off := 0; off < len(pcm); off += frame {
		scores, err := e.Process(pcm[off:min(off+frame, len(pcm))])
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range scores {
			peak = max(peak, v)
			seq = append(seq, fmt.Sprintf("%.3f", v))
		}
	}

	t.Logf("peak %.4f over %d scores  model %s", peak, len(seq), model)
	if os.Getenv("ONE_SEQ") != "" {
		t.Logf("scores: %s", strings.Join(seq, " "))
	}
}
