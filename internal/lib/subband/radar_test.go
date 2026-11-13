package subband

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeCoefs(t *testing.T, path string, values []float32) {
	t.Helper()
	var b strings.Builder
	b.WriteString("/* a comment line, as the vendor's files open with */\n")
	for _, v := range values {
		fmt.Fprintf(&b, "%g,\n", v)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func radarDir(t *testing.T, weights []float32) string {
	t.Helper()
	g := radarFBFV2()
	dir := t.TempDir()
	writeCoefs(t, filepath.Join(dir, g.windowFile), make([]float32, g.WindowLen))
	writeCoefs(t, filepath.Join(dir, g.weightsFile), weights)
	return dir
}

func radarWeightCount() int {
	g := radarFBFV2()
	return g.Bands * g.Beams * g.Taps * g.Inputs() * 2
}

func TestRadarsCoefficientsLoad(t *testing.T) {
	w, err := Load(radarDir(t, make([]float32, radarWeightCount())))
	if err != nil {
		t.Fatalf("radar's coefficient set did not load: %v", err)
	}
	if w.Name() != radarFBFV2().name {
		t.Errorf("loaded %q, want radar's %q", w.Name(), radarFBFV2().name)
	}
}

func TestRadarUsesItsOwnSquareOfTheRing(t *testing.T) {
	if got := radarFBFV2().mics; !slices.Equal(got, []int{0, 1, 3, 4}) {
		t.Errorf("radar beamforms on mics %v, want [0 1 3 4]", got)
	}
	if got := fbfV2.mics; !slices.Equal(got, []int{1, 2, 4, 5}) {
		t.Errorf("deriving radar's geometry changed fbfV2's mics to %v", got)
	}
}

func TestRadarBoostsByItsOwnGain(t *testing.T) {
	if got := radarFBFV2().boostDB; got != 2.51 {
		t.Errorf("radar boosts by %g dB, want 2.51", got)
	}
	if fbfV2.boostDB != 7.2 {
		t.Errorf("deriving radar's geometry changed fbfV2's boost to %g", fbfV2.boostDB)
	}
}

func TestRadarWeightsDecodeInTheVendorsOrder(t *testing.T) {
	g := radarFBFV2()
	const beam, mic, band, tap = 2, 3, 5, 1
	at := (((beam*g.Inputs()+mic)*g.Bands+band)*g.Taps + tap) * 2

	values := make([]float32, radarWeightCount())
	values[at], values[at+1] = 0.25, -0.5

	w, err := Load(radarDir(t, values))
	if err != nil {
		t.Fatal(err)
	}
	if got := w.weight(band, beam, tap)[mic]; got != complex(0.25, -0.5) {
		t.Errorf("the planted weight decoded as %v at band %d beam %d tap %d mic %d",
			got, band, beam, tap, mic)
	}
}

func TestRadarRefusesAShortWeightsFile(t *testing.T) {
	if _, err := Load(radarDir(t, make([]float32, radarWeightCount()-1))); err == nil {
		t.Fatal("a weights file one coefficient short loaded")
	}
}
