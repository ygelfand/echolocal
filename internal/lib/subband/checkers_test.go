package subband

import (
	"path/filepath"
	"slices"
	"testing"
)

// checkersDir lays out what checkers' firmware ships: FBFV2 weights for 7 beams under their own
// file name, beside a coefs_FBF.cfg.
func checkersDir(t *testing.T, weights []float32) string {
	t.Helper()
	g := checkersFBFV2()
	dir := t.TempDir()
	writeCoefs(t, filepath.Join(dir, g.windowFile), make([]float32, g.WindowLen))
	writeCoefs(t, filepath.Join(dir, g.weightsFile), weights)

	fbf := known[0]
	writeCoefs(t, filepath.Join(dir, fbf.windowFile), make([]float32, fbf.WindowLen))
	writeCoefs(t, filepath.Join(dir, fbf.weightsFile), make([]float32, 64*8*4*2*2))
	return dir
}

func checkersWeightCount() int {
	g := checkersFBFV2()
	return g.Bands * g.Beams * g.Taps * g.Inputs() * 2
}

func TestCheckersLoadsItsOwnTuning(t *testing.T) {
	w, err := Load(checkersDir(t, make([]float32, checkersWeightCount())), []Tuning{CheckersFBFV2})
	if err != nil {
		t.Fatalf("checkers' coefficient set did not load: %v", err)
	}
	if w.Name() != string(CheckersFBFV2) || w.Beams() != 7 || w.Inputs() != 2 {
		t.Errorf("loaded %q with %d beams over %d mics, want checkers' with 7 over 2",
			w.Name(), w.Beams(), w.Inputs())
	}
}

func TestCheckersWeightsAreTheVendorsSize(t *testing.T) {
	if got := checkersWeightCount(); got != 14336 {
		t.Errorf(
			"checkers' tuning reads %d values, want the 14336 in coefs_FBFV2_LowLatency_7beams.cfg",
			got,
		)
	}
}

func TestCheckersGroupsDoNotWrap(t *testing.T) {
	g := checkersFBFV2()
	want := [][]int{{0, 1, 1}, {0, 1, 2}, {1, 2, 3}, {2, 3, 4}, {3, 4, 5}, {4, 5, 6}, {5, 5, 6}}
	if !slices.EqualFunc(g.groups, want, slices.Equal) {
		t.Errorf("checkers groups beams as %v, want AFE.cfg's %v", g.groups, want)
	}
	if g.margin != 1.26 || g.hold != 12 {
		t.Errorf("checkers merges with margin %g, hold %d; want 1.26, 12", g.margin, g.hold)
	}
	if len(fbfV2.groups) != 8 || fbfV2.Beams != 8 {
		t.Errorf(
			"deriving checkers' geometry changed fbfV2 to %d beams in %d groups",
			fbfV2.Beams,
			len(fbfV2.groups),
		)
	}
}

func TestCheckersBoostsByItsOwnGain(t *testing.T) {
	if got := checkersFBFV2().boostDB; got != 4.3 {
		t.Errorf("checkers boosts by %g dB, want 4.3", got)
	}
	if fbfV2.boostDB != 7.2 {
		t.Errorf("deriving checkers' geometry changed fbfV2's boost to %g", fbfV2.boostDB)
	}
}

func TestCheckersWeightsDecodeInTheVendorsOrder(t *testing.T) {
	g := checkersFBFV2()
	const beam, mic, band, tap = 5, 1, 9, 3
	at := (((beam*g.Inputs()+mic)*g.Bands+band)*g.Taps + tap) * 2

	values := make([]float32, checkersWeightCount())
	values[at], values[at+1] = -0.75, 0.125

	w, err := Load(checkersDir(t, values), []Tuning{CheckersFBFV2})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.weight(band, beam, tap)[mic]; got != complex(-0.75, 0.125) {
		t.Errorf("the planted weight decoded as %v at band %d beam %d tap %d mic %d",
			got, band, beam, tap, mic)
	}
}
