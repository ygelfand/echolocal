package subband

import (
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"testing"
)

// TestSearchLayout is scaffolding, not a check. The Fire OS 6 coefficient file carries no comments
// describing its layout, and reading it with Fire OS 5's does not steer, so this drives it through
// every plausible ordering and reports which ones do.
//
// The fitness is the same question direction_test asks: does a different beam win at each bearing?
// A correct layout answers with every beam, as Fire OS 5's does; a wrong one piles the energy into
// one or two whatever the source does.
//
//	ECHOLOCAL_FBF=/tmp/coefs/coefs_FBFV2_LowLatency_8beams.cfg \
//	ECHOLOCAL_WINDOW=/tmp/coefs/coefs_FilterBank_AnalysisSynthesis_768cvxGLow.cfg \
//	go test ./internal/lib/subband/ -run SearchLayout -v
func TestSearchLayout(t *testing.T) {
	weights, window := os.Getenv("ECHOLOCAL_FBF"), os.Getenv("ECHOLOCAL_WINDOW")
	if weights == "" || window == "" {
		t.Skip("set ECHOLOCAL_FBF and ECHOLOCAL_WINDOW")
	}

	// Defaults are Fire OS 6's, from its AFE.cfg with bands at half the transform as its own comment
	// requires. Fire OS 5's numbers go in through the environment, to calibrate the score against a
	// layout already known to be right.
	base := geometry{
		Bands:     envInt("ECHOLOCAL_BANDS", 128),
		FFTLen:    envInt("ECHOLOCAL_FFT", 256),
		Hop:       envInt("ECHOLOCAL_HOP", 128),
		WindowLen: envInt("ECHOLOCAL_WINDOWLEN", 768),
		Beams:     envInt("ECHOLOCAL_BEAMS", 8),
		Taps:      4,
		perGroup:  4,
		boostDB:   7.2,
	}

	win, err := readFloats(window, base.WindowLen)
	if err != nil {
		t.Fatal(err)
	}
	nmics := envInt("ECHOLOCAL_MICS", 4)
	values, err := readFloats(weights, base.Bands*base.Beams*base.Taps*nmics*2)
	if err != nil {
		t.Fatal(err)
	}

	maps := map[string][]int{"all": ring(nmics)}
	if nmics < arrayMics {
		maps = map[string][]int{}
		for _, pick := range orderedPicks(arrayMics, nmics) {
			maps[fmt.Sprint(pick)] = pick
		}
	}
	t.Logf("%d microphone orderings", len(maps))

	type result struct {
		how  string
		won  float64
		beam []int
	}
	var best []result

	for _, order := range []string{"btm", "bmt", "tbm", "tmb", "mbt", "mtb"} {
		for _, down := range []bool{true, false} {
			for _, split := range []bool{true, false} {
				for name, mics := range maps {
					g := base
					g.mics = mics

					w := &Weights{g: g, window: win}
					if !fill(w, values, order, down, split) {
						continue
					}
					won, beams := steers(w)
					best = append(best, result{
						how:  fmt.Sprintf("%s down=%-5v split=%-5v mics=%s", order, down, split, name),
						won:  won,
						beam: beams,
					})
				}
			}
		}
	}

	sort.Slice(best, func(i, j int) bool { return best[i].won > best[j].won })
	for _, r := range best[:min(12, len(best))] {
		t.Logf("%5.3f   %s   %v", r.won, r.how, r.beam)
	}
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return def
}

// ring is the first n microphones of the array.
func ring(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// orderedPicks is every way to take k of n microphones in some order, which is what the coefficient
// file's microphone axis could mean.
func orderedPicks(n, k int) [][]int {
	if k == 0 {
		return [][]int{{}}
	}
	var out [][]int
	for _, rest := range orderedPicks(n, k-1) {
		for m := range n {
			if slices.Contains(rest, m) {
				continue
			}
			out = append(out, append(append([]int{}, rest...), m))
		}
	}
	return out
}

// fill lays the file out under one candidate ordering. order names the three axes outer to inner,
// down counts taps backwards the way Fire OS 5 does, and split puts a block's real parts before its
// imaginary ones rather than interleaving them.
func fill(w *Weights, values []float32, order string, down, split bool) bool {
	g := w.g
	size := map[byte]int{'b': g.Beams, 't': g.Taps, 'm': g.Inputs()}
	w.fbf = make([]complex64, g.Bands*g.Beams*g.Taps*g.Inputs())

	at := 0
	for group := range g.Bands / g.perGroup {
		for i0 := range size[order[0]] {
			for i1 := range size[order[1]] {
				for i2 := range size[order[2]] {
					pos := map[byte]int{order[0]: i0, order[1]: i1, order[2]: i2}
					beam, tap, mic := pos['b'], pos['t'], pos['m']
					if down {
						tap = g.Taps - 1 - tap
					}

					if at+2*g.perGroup > len(values) {
						return false
					}
					block := values[at : at+2*g.perGroup]
					at += 2 * g.perGroup

					for b := range g.perGroup {
						re, im := block[b], block[g.perGroup+b]
						if !split {
							re, im = block[2*b], block[2*b+1]
						}
						w.weight(group*g.perGroup+b, beam, tap)[mic] = complex(re, im)
					}
				}
			}
		}
	}
	return at == len(values)
}

// steers reports how sharply the beams discriminate: the loudest beam against the average of them
// all, meaned over a circle of bearings. A layout read correctly answers a source differently
// depending on where it is; one read wrong gives the same flat pattern whatever the source does.
func steers(w *Weights) (float64, []int) {
	var contrast float64
	seen := map[int]bool{}
	var order []int

	for step := range 12 {
		bearing := float64(step) * 30 * math.Pi / 180

		// An eighth input would be the echo canceller's reference, silent with nothing playing.
		frame := planeWave(bearing, 16*testFrame)
		for len(frame) < w.g.Channels() {
			frame = append(frame, make([]int16, 16*testFrame))
		}

		b := w.New()
		b.Mix(frame)

		loudest, total := 0, float64(0)
		for j, e := range b.energy {
			total += float64(e)
			if e > b.energy[loudest] {
				loudest = j
			}
		}
		if total > 0 {
			contrast += float64(b.energy[loudest]) / (total / float64(len(b.energy)))
		}
		seen[loudest] = true
		order = append(order, loudest)
	}
	// Distinct winners is what separates a layout that steers from one that does not; contrast only
	// breaks ties between layouts that steer equally widely.
	return float64(len(seen)) + contrast/1000, order
}
