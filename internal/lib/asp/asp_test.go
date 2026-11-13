package asp

import (
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ygelfand/libcountertop/pkg/audio/fft"
)

func convolve(x, h []float32) []float32 {
	out := make([]float32, len(x))
	for n := range x {
		var sum float64
		for k, c := range h {
			if n-k >= 0 {
				sum += float64(c) * float64(x[n-k])
			}
		}
		out[n] = float32(sum)
	}
	return out
}

func TestFIRMatchesDirectConvolution(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	h := make([]float32, 256)
	for i := range h {
		h[i] = float32(r.NormFloat64()) * float32(math.Exp(-float64(i)/40))
	}

	const block = 64
	x := make([]float32, block*9)
	for i := range x {
		x[i] = float32(r.NormFloat64())
	}
	want := convolve(x, h)

	got := make([]float32, len(x))
	copy(got, x)
	f := newFIR(h, block)
	for i := 0; i < len(got); i += block {
		f.process(got[i : i+block])
	}

	for i := range want {
		if diff := math.Abs(float64(want[i] - got[i])); diff > 1e-3 {
			t.Fatalf("sample %d: direct convolution %g, overlap-save %g", i, want[i], got[i])
		}
	}
}

func TestCrossoverSumsFlat(t *testing.T) {
	const (
		rate  = Rate
		n     = 8192
		bands = 4
	)
	fc := []float64{115, 500, 7500}

	x := make([]float32, n)
	x[0] = 1

	c := newCrossover(fc, rate)
	out := make([][]float32, bands)
	for i := range out {
		out[i] = make([]float32, n)
	}
	c.process(x, out)

	sum := make([]complex64, n)
	for i := range sum {
		var v float32
		for _, b := range out {
			v += b[i]
		}
		sum[i] = complex(v, 0)
	}
	fft.New(n).Forward(sum)

	for k := 1; k < n/2; k++ {
		hz := float64(k) * rate / n
		if hz < 20 || hz > 18000 {
			continue
		}
		db := 20 * math.Log10(float64(real(sum[k]*conj(sum[k])))/2*0+cmagf(sum[k]))
		if math.Abs(db) > 0.5 {
			t.Fatalf("%.0f Hz: the bands sum to %+.2f dB", hz, db)
		}
	}
}

func conj(c complex64) complex64 { return fft.Conj(c) }

func cmagf(c complex64) float64 {
	return math.Hypot(float64(real(c)), float64(imag(c)))
}

func TestFullBandLimiterHoldsCeiling(t *testing.T) {
	tuning := stepped(unitTaps())
	c, err := tuning.Chain(512)
	if err != nil {
		t.Fatal(err)
	}

	r := rand.New(rand.NewSource(2))
	ceil := math.Pow(10, testMBCL().Full.LimThresh/20)
	for range 40 {
		x := make([]float32, 512)
		for i := range x {
			x[i] = float32(r.NormFloat64()) * 8
		}
		c.Process(x, 1)
		for i, v := range x {
			if math.Abs(float64(v)) > ceil+1e-6 {
				t.Fatalf("sample %d left the chain at %g, ceiling is %g", i, v, ceil)
			}
		}
	}
}

// The vendor tuning compresses the band below 115 Hz 20:1 from -50 dB.
func TestLowBandIsHeldDown(t *testing.T) {
	tuning := stepped(unitTaps())
	c, err := tuning.Chain(512)
	if err != nil {
		t.Fatal(err)
	}

	var peak float64
	for b := range 60 {
		x := make([]float32, 512)
		for i := range x {
			n := float64(b*512 + i)
			x[i] = float32(0.9 * math.Sin(2*math.Pi*40*n/Rate))
		}
		c.Process(x, 1)
		if b < 30 {
			continue
		}
		for _, v := range x {
			peak = math.Max(peak, math.Abs(float64(v)))
		}
	}

	if db := 20 * math.Log10(peak); db > -30 {
		t.Errorf("40 Hz at -1 dBFS came through at %.1f dBFS, the low band is not being held", db)
	}
}

func unitTaps() []float32 {
	h := make([]float32, 8)
	h[0] = 1
	return h
}

func testMBCL() mbcl {
	band := mbclBand{CompRatio: 2, CompThresh: -10, CompGainMin: -40, LimThresh: 0, LimRelease: 1}
	low := mbclBand{
		CompRatio:   20,
		CompThresh:  -50,
		CompGainMin: -40,
		LimThresh:   -8,
		LimRelease:  200,
	}
	return mbcl{
		PreFilterBypass: true,
		NumBands:        4,
		Crossovers:      []float64{115, 500, 7500},
		Bands:           []mbclBand{low, band, band, band},
		Full:            mbclLimit{LimThresh: -0.1, LimRelease: 200},
	}
}

// The vendor tuning is not ours to ship.
func vendorTuning(t *testing.T) *Tuning {
	t.Helper()
	dir := os.Getenv("ECHOLOCAL_VENDOR_DIR")
	if dir == "" {
		t.Skip("set ECHOLOCAL_VENDOR_DIR to a copy of /vendor/etc/audio-algorithms")
	}

	v, err := Load(dir, dotFiles)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return v
}

func TestVendorFilterHasTheShapeWeMeasured(t *testing.T) {
	v := vendorTuning(t)

	const n = 8192
	spec := make([]complex64, n)
	for i, c := range v.taps[0] {
		spec[i] = complex(c, 0)
	}
	fft.New(n).Forward(spec)

	band := func(lo, hi float64) float64 {
		var sum float64
		var count int
		for k := 1; k < n/2; k++ {
			if hz := float64(k) * Rate / n; hz >= lo && hz < hi {
				sum += cmagf(spec[k])
				count++
			}
		}
		return 20 * math.Log10(sum/float64(count))
	}

	ref := band(500, 1000)
	for _, want := range []struct {
		lo, hi   float64
		min, max float64
	}{
		{125, 200, 22, 30},
		{200, 320, 16, 28},
		{2000, 3200, -16, -6},
		{500, 1000, -1, 1},
	} {
		if got := band(want.lo, want.hi) - ref; got < want.min || got > want.max {
			t.Errorf("%.0f-%.0f Hz is %+.1f dB, expected between %+.0f and %+.0f",
				want.lo, want.hi, got, want.min, want.max)
		}
	}
}

func TestVendorMBCLIsTheOneWeBuiltFor(t *testing.T) {
	v := vendorTuning(t)
	m := v.mbcl

	if len(m.Bands) != 4 || len(m.Crossovers) != 3 {
		t.Fatalf("%d bands and %d crossovers", len(m.Bands), len(m.Crossovers))
	}
	if m.Crossovers[0] != 115 {
		t.Errorf(
			"the low crossover is at %g Hz, the driver protection assumes 115",
			m.Crossovers[0],
		)
	}
	if b := m.Bands[0]; b.CompRatio < 10 || b.CompThresh > -40 {
		t.Errorf("the low band compresses %g:1 from %g dB, which will not hold the bass boost",
			b.CompRatio, b.CompThresh)
	}
	if _, err := v.Chain(1024); err != nil {
		t.Errorf("the real tuning does not build a chain: %v", err)
	}
}

func TestLoadRejectsAWrongLengthFilter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, dotFiles.EQ[0].Name),
		[]byte("1.0,\n2.0,\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, dotFiles); err == nil {
		t.Fatal("a two-tap filter loaded as if it were the tuning")
	}
}

func writeTuning(t *testing.T, files Files, n int) string {
	t.Helper()
	dir := t.TempDir()
	var body strings.Builder
	for i := range n {
		v := "0.0"
		if i == 0 {
			v = "1.0"
		}
		body.WriteString(v + ",\n")
	}
	for _, s := range files.EQ {
		if err := os.WriteFile(
			filepath.Join(dir, s.Name),
			[]byte(body.String()),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	mbcl, err := json.Marshal(testMBCL())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, files.Limiter), mbcl, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAnUnsetTapCountIsTheUsualOne(t *testing.T) {
	tuning, err := Load(writeTuning(t, dotFiles, defaultTaps), dotFiles)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(tuning.taps[0]); got != defaultTaps {
		t.Errorf("loaded %d taps, want %d", got, defaultTaps)
	}
}

func TestABoardCanAskForLongerFilters(t *testing.T) {
	files := dotFiles
	files.Taps = 2048

	tuning, err := Load(writeTuning(t, files, 2048), files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tuning.Chain(384); err != nil {
		t.Fatalf("a 2048-tap tuning does not build a chain: %v", err)
	}
}

func TestLongerFiltersAreRefusedWhereTheBoardDoesNotExpectThem(t *testing.T) {
	if _, err := Load(writeTuning(t, dotFiles, 2048), dotFiles); err == nil {
		t.Fatal("a 2048-tap filter loaded on a board that ships 1024")
	}
}

func TestANegativeTapCountIsRefused(t *testing.T) {
	files := dotFiles
	files.Taps = -1
	if _, err := Load(t.TempDir(), files); err == nil {
		t.Fatal("a negative tap count was accepted")
	}
}

func TestNothingListedLoadsNothing(t *testing.T) {
	if _, err := Load(t.TempDir(), Files{}); err == nil {
		t.Fatal("a board without tuning files loaded a tuning")
	}
}

func TestEachVolumeUsesItsOwnFilter(t *testing.T) {
	tuning := stepped([]float32{1}, []float32{0.5}, []float32{0.25})
	for _, c := range []struct {
		share float64
		want  int
	}{
		{0, 0}, {0.3, 0}, {0.31, 1}, {0.6, 1}, {0.61, 2}, {1, 2}, {1.5, 2},
	} {
		if got := tuning.step(c.share); got != c.want {
			t.Errorf("volume %.2f uses filter %d, want %d", c.share, got, c.want)
		}
	}
}

func TestAStepChangeCrossfadesWithoutAJump(t *testing.T) {
	const block = 64
	c, err := stepped([]float32{1}, []float32{0.5}, []float32{0.25}).Chain(block)
	if err != nil {
		t.Fatal(err)
	}

	run := func(share float64) []float32 {
		x := make([]float32, block)
		for i := range x {
			x[i] = 0.1
		}
		c.filter(x, share)
		return x
	}
	run(0.1)
	before := run(0.1)
	during := run(0.5)
	after := run(0.5)

	if math.Abs(float64(during[0]-before[block-1])) > 0.1*0.5/block+1e-6 {
		t.Errorf(
			"the first sample after the change jumped from %g to %g",
			before[block-1],
			during[0],
		)
	}
	if math.Abs(float64(during[block-1]-after[0])) > 1e-6 {
		t.Errorf("the crossfade ends at %g, the new filter plays %g", during[block-1], after[0])
	}
	if math.Abs(float64(after[0])-0.05) > 1e-6 {
		t.Errorf("after the change the filter plays %g, want the second step's 0.05", after[0])
	}
}

func TestTheNewFilterHearsThePast(t *testing.T) {
	const block = 32
	echo := make([]float32, 40)
	echo[36] = 1
	c, err := stepped(echo, echo).Chain(block)
	if err != nil {
		t.Fatal(err)
	}

	x := make([]float32, block)
	x[0] = 1
	c.filter(x, 0.1)
	quiet := make([]float32, block)
	c.filter(quiet, 1)
	if math.Abs(float64(quiet[4])-1) > 1e-3 {
		t.Errorf("an impulse from before the change came out at %g, want 1", quiet[4])
	}
}

func TestInputGainsFollowTheVendorDefinitions(t *testing.T) {
	m := testMBCL()
	m.InVol = 6
	for i := range m.Bands {
		m.Bands[i] = mbclBand{CompRatio: 1, CompGainMin: -40, LimThresh: 0, LimRelease: 1}
	}
	m.Full = mbclLimit{LimThresh: 0, LimRelease: 1}
	s, err := newMBCL("test", m, Rate)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s.in, float32(math.Pow(10, 6.0/20)); math.Abs(float64(got-want)) > 1e-6 {
		t.Errorf("inVol 6 dB is a gain of %g, want %g", got, want)
	}

	m.Bands[0].CompInVol, m.Bands[1].LimInVol = 6, -6
	s, err = newMBCL("test", m, Rate)
	if err != nil {
		t.Fatal(err)
	}
	if s.bands[0].compIn < 1.99 || s.bands[1].limIn > 0.51 {
		t.Errorf("band input gains are %g and %g", s.bands[0].compIn, s.bands[1].limIn)
	}
}

func TestARatioBelowOneDoesNotCompress(t *testing.T) {
	m := testMBCL()
	m.Bands[2].CompRatio, m.Bands[2].CompThresh = 0, 0
	s, err := newMBCL("test", m, Rate)
	if err != nil {
		t.Fatalf("a ratio of 0 was refused: %v", err)
	}
	if s.bands[2].compRatio != 1 {
		t.Errorf("a ratio of 0 compresses at %g:1", s.bands[2].compRatio)
	}
}

var dotFiles = Files{
	EQ: []Step{
		{UpTo: 0.5, Name: "EQ_50.cfg"},
		{UpTo: 0.6, Name: "EQ_60.cfg"},
		{UpTo: 0.7, Name: "EQ_70.cfg"},
		{UpTo: 0.8, Name: "EQ_80.cfg"},
		{UpTo: 0.9, Name: "EQ_90.cfg"},
		{UpTo: 1.0, Name: "EQ_100.cfg"},
	},
	Limiter: "MBCL.cfg",
}

func stepped(filters ...[]float32) *Tuning {
	t := &Tuning{mbcl: testMBCL(), files: Files{Limiter: "test"}}
	for i, h := range filters {
		t.files.EQ = append(t.files.EQ, Step{UpTo: 0.3 * float64(i+1)})
		t.taps = append(t.taps, h)
	}
	return t
}

func TestStripComments(t *testing.T) {
	in := []byte(`{
		// a line comment
		"FilterBank FC": [115], /* and a block one */
		"Name": "http://not-a-comment"
	}`)
	m := map[string]any{}
	if err := json.Unmarshal(stripComments(in), &m); err != nil {
		t.Fatalf("stripped to something unparseable: %v\n%s", err, stripComments(in))
	}
	if m["Name"] != "http://not-a-comment" {
		t.Errorf("the // inside a string was treated as a comment: %v", m["Name"])
	}
}
