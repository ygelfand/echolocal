package subband

import (
	"path/filepath"
	"slices"
	"testing"
)

// athenaDir lays out what athena's firmware ships: its own FBFV2 weights under fbfV2's file name,
// beside a crown coefs_FBF.cfg holding 4 mics where the FBF geometry expects 7.
func athenaDir(t *testing.T, weights []float32) string {
	t.Helper()
	g := athenaFBFV2()
	dir := t.TempDir()
	writeCoefs(t, filepath.Join(dir, g.windowFile), make([]float32, g.WindowLen))
	writeCoefs(t, filepath.Join(dir, g.weightsFile), weights)

	fbf := known[0]
	writeCoefs(t, filepath.Join(dir, fbf.windowFile), make([]float32, fbf.WindowLen))
	writeCoefs(t, filepath.Join(dir, fbf.weightsFile), make([]float32, 64*8*4*4*2))
	return dir
}

func athenaWeightCount() int {
	g := athenaFBFV2()
	return g.Bands * g.Beams * g.Taps * g.Inputs() * 2
}

func TestAthenaLoadsItsOwnTuning(t *testing.T) {
	w, err := Load(athenaDir(t, make([]float32, athenaWeightCount())), AthenaFBFV2)
	if err != nil {
		t.Fatalf("athena's coefficient set did not load: %v", err)
	}
	if w.Name() != string(AthenaFBFV2) {
		t.Errorf("loaded %q, want athena's %q", w.Name(), AthenaFBFV2)
	}
}

// Under the dot's tunings the same folder fails on crown's coefs_FBF.cfg, and without that file
// it loads fbfV2's mic map: the reason a board must name its tunings.
func TestAthenasFolderIsMisreadUnderTheDotsTunings(t *testing.T) {
	if _, err := Load(athenaDir(t, make([]float32, athenaWeightCount())), dotTunings...); err == nil {
		t.Error("crown's 4-mic coefs_FBF.cfg loaded as the dot's 7-mic FBF")
	}

	g := athenaFBFV2()
	dir := t.TempDir()
	writeCoefs(t, filepath.Join(dir, g.windowFile), make([]float32, g.WindowLen))
	writeCoefs(t, filepath.Join(dir, g.weightsFile), make([]float32, athenaWeightCount()))
	w, err := Load(dir, dotTunings...)
	if err != nil {
		t.Fatal(err)
	}
	if w.Name() != string(FBFV2) {
		t.Errorf("loaded %q, want fbfV2 under the dot's tunings", w.Name())
	}
}

func TestAthenaBeamformsOnItsFourMics(t *testing.T) {
	g := athenaFBFV2()
	if !slices.Equal(g.mics, []int{0, 1, 2, 3}) {
		t.Errorf("athena beamforms on mics %v, want [0 1 2 3]", g.mics)
	}
	if g.Channels() != 4 {
		t.Errorf("athena's tuning reads %d channels, want only its 4 mics", g.Channels())
	}
	if !slices.Equal(fbfV2.mics, []int{1, 2, 4, 5}) {
		t.Errorf("deriving athena's geometry changed fbfV2's mics to %v", fbfV2.mics)
	}
}

func TestAthenaBoostsByItsOwnGain(t *testing.T) {
	if got := athenaFBFV2().boostDB; got != 2.2 {
		t.Errorf("athena boosts by %g dB, want 2.2", got)
	}
	if fbfV2.boostDB != 7.2 {
		t.Errorf("deriving athena's geometry changed fbfV2's boost to %g", fbfV2.boostDB)
	}
}

func TestAthenaSharesFBFV2sFrontEnd(t *testing.T) {
	g := athenaFBFV2()
	if g.weightsFile != fbfV2.weightsFile || g.windowFile != fbfV2.windowFile {
		t.Errorf("athena reads %s and %s, want fbfV2's %s and %s",
			g.weightsFile, g.windowFile, fbfV2.weightsFile, fbfV2.windowFile)
	}
	if g.margin != 1.26 || g.hold != 12 || len(g.groups) != 8 {
		t.Errorf("athena merges with margin %g, hold %d, %d groups; want 1.26, 12, 8",
			g.margin, g.hold, len(g.groups))
	}
}

func TestAthenaWeightsDecodeInTheVendorsOrder(t *testing.T) {
	g := athenaFBFV2()
	const beam, mic, band, tap = 6, 2, 9, 3
	at := (((beam*g.Inputs()+mic)*g.Bands+band)*g.Taps + tap) * 2

	values := make([]float32, athenaWeightCount())
	values[at], values[at+1] = -0.75, 0.125

	w, err := Load(athenaDir(t, values), AthenaFBFV2)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.weight(band, beam, tap)[mic]; got != complex(-0.75, 0.125) {
		t.Errorf("the planted weight decoded as %v at band %d beam %d tap %d mic %d",
			got, band, beam, tap, mic)
	}
}
