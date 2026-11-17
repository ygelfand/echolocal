package subband

import (
	"path/filepath"
	"slices"
	"testing"
)

// crownDir lays out what crown's firmware ships: FBFV2 weights under fbfV2's file name, beside its
// own 4-mic coefs_FBF.cfg.
func crownDir(t *testing.T, weights []float32) string {
	t.Helper()
	g := crownFBFV2()
	dir := t.TempDir()
	writeCoefs(t, filepath.Join(dir, g.windowFile), make([]float32, g.WindowLen))
	writeCoefs(t, filepath.Join(dir, g.weightsFile), weights)

	fbf := known[0]
	writeCoefs(t, filepath.Join(dir, fbf.windowFile), make([]float32, fbf.WindowLen))
	writeCoefs(t, filepath.Join(dir, fbf.weightsFile), make([]float32, 64*8*4*4*2))
	return dir
}

func crownWeightCount() int {
	g := crownFBFV2()
	return g.Bands * g.Beams * g.Taps * g.Inputs() * 2
}

func TestCrownLoadsItsOwnTuning(t *testing.T) {
	w, err := Load(crownDir(t, make([]float32, crownWeightCount())), []Tuning{CrownFBFV2})
	if err != nil {
		t.Fatalf("crown's coefficient set did not load: %v", err)
	}
	if w.Name() != string(CrownFBFV2) {
		t.Errorf("loaded %q, want crown's %q", w.Name(), CrownFBFV2)
	}
}

func TestCrownBeamformsOnItsFourMics(t *testing.T) {
	g := crownFBFV2()
	if !slices.Equal(g.mics, []int{0, 1, 2, 3}) {
		t.Errorf("crown beamforms on mics %v, want [0 1 2 3]", g.mics)
	}
	if g.Channels() != 4 {
		t.Errorf("crown's tuning reads %d channels, want only its 4 mics", g.Channels())
	}
}

func TestCrownBoostsByItsOwnGain(t *testing.T) {
	if got := crownFBFV2().boostDB; got != 1.9 {
		t.Errorf("crown boosts by %g dB, want 1.9", got)
	}
	if got := athenaFBFV2().boostDB; got != 2.2 {
		t.Errorf("deriving crown's geometry changed athena's boost to %g", got)
	}
}

func TestCrownSharesAthenasFrontEnd(t *testing.T) {
	g, a := crownFBFV2(), athenaFBFV2()
	if g.weightsFile != a.weightsFile || g.windowFile != a.windowFile {
		t.Errorf("crown reads %s and %s, want athena's %s and %s",
			g.weightsFile, g.windowFile, a.weightsFile, a.windowFile)
	}
	if g.margin != 1.26 || g.hold != 12 || len(g.groups) != 8 {
		t.Errorf("crown merges with margin %g, hold %d, %d groups; want 1.26, 12, 8",
			g.margin, g.hold, len(g.groups))
	}
}
