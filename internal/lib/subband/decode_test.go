package subband

import (
	"fmt"
	"os"
	"sort"
	"testing"
)

// TestDecodeV2 reads the Fire OS 6 weights under the layout its own structure describes.
//
// Correlating the file against itself at every stride shows the sign alternating every 16 complex
// values and decaying slowly out to 128 — a long smooth axis carrying the modulation a DFT filter
// bank puts on adjacent bands. So bands step every 16, 16 is taps times microphones, and the 128
// bands of a beam occupy 2048 of the 16384 complex values. Adjacent values are uncorrelated where
// Fire OS 5's are not, which is real and imaginary interleaved rather than split.
//
//	ECHOLOCAL_FBF=... ECHOLOCAL_WINDOW=... go test ./internal/lib/subband/ -run DecodeV2 -v
func TestDecodeV2(t *testing.T) {
	weights, window := os.Getenv("ECHOLOCAL_FBF"), os.Getenv("ECHOLOCAL_WINDOW")
	if weights == "" || window == "" {
		t.Skip("set ECHOLOCAL_FBF and ECHOLOCAL_WINDOW")
	}

	base := geometry{Bands: 128, FFTLen: 256, Hop: 128, WindowLen: 768, Beams: 8, Taps: 4, boostDB: 7.2}

	win, err := readFloats(window, base.WindowLen)
	if err != nil {
		t.Fatal(err)
	}
	values, err := readFloats(weights, base.Bands*base.Beams*base.Taps*4*2)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		how  string
		won  float64
		beam []int
	}
	var out []result

	for _, order := range []string{"maet"} {
		for _, down := range []bool{false, true} {
			for _, flip := range []bool{false} {
				for _, mics := range micOrders(t) {
					g := base
					g.mics = mics

					w := &Weights{g: g, window: win}
					if !fill4(w, values, order, down, flip) {
						continue
					}
					won, beams := steers(w)
					if won >= 6 {
						fmt.Printf("  %5.3f  %s down=%v mics=%v %v\n", won, order, down, mics, beams)
					}
					out = append(out, result{
						fmt.Sprintf("%s down=%-5v flip=%-5v mics=%v", order, down, flip, mics),
						won, beams})
				}
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].won > out[j].won })
	for _, r := range out[:min(15, len(out))] {
		t.Logf("%6.3f   %s   %v", r.won, r.how, r.beam)
	}
}

// micOrders is what to try for the microphone axis: the map AFE.cfg names first, then every ordered
// choice of four when ECHOLOCAL_ALLMICS is set.
func micOrders(t *testing.T) [][]int {
	if os.Getenv("ECHOLOCAL_ALLMICS") != "" {
		return orderedPicks(arrayMics, 4)
	}
	return [][]int{{1, 2, 4, 5}, {0, 1, 3, 4}, {0, 1, 2, 3}}
}

// fill4 lays the file out with all four axes free and one complex value per element, real and
// imaginary interleaved. order names the axes outer to inner: a band, e beam, t tap, m microphone.
func fill4(w *Weights, values []float32, order string, down, flip bool) bool {
	g := w.g
	size := map[byte]int{'a': g.Bands, 'e': g.Beams, 't': g.Taps, 'm': g.Inputs()}
	w.fbf = make([]complex64, g.Bands*g.Beams*g.Taps*g.Inputs())

	at := 0
	for i0 := range size[order[0]] {
		for i1 := range size[order[1]] {
			for i2 := range size[order[2]] {
				for i3 := range size[order[3]] {
					pos := map[byte]int{order[0]: i0, order[1]: i1, order[2]: i2, order[3]: i3}
					tap := pos['t']
					if down {
						tap = g.Taps - 1 - tap
					}
					if at+2 > len(values) {
						return false
					}
					re, im := values[at], values[at+1]
					at += 2
					// Their bank may carry a half-band modulation ours does not, which shows as the
					// sign alternating from one band to the next.
					if flip && pos['a']%2 == 1 {
						re, im = -re, -im
					}
					w.weight(pos['a'], pos['e'], tap)[pos['m']] = complex(re, im)
				}
			}
		}
	}
	return at == len(values)
}

// TestBeamEnergiesPairUp asks whether eight beams over four microphones are four patterns and their
// negations. If they are, a beam and its opposite have identical energy, at most four directions can
// ever win, and scoring a layout by how many of the eight win is measuring the wrong thing.
func TestBeamEnergiesPairUp(t *testing.T) {
	weights, window := os.Getenv("ECHOLOCAL_FBF"), os.Getenv("ECHOLOCAL_WINDOW")
	if weights == "" || window == "" {
		t.Skip("set ECHOLOCAL_FBF and ECHOLOCAL_WINDOW")
	}
	base := geometry{Bands: 128, FFTLen: 256, Hop: 128, WindowLen: 768, Beams: 8, Taps: 4, boostDB: 7.2}
	win, err := readFloats(window, base.WindowLen)
	if err != nil {
		t.Fatal(err)
	}
	values, err := readFloats(weights, base.Bands*base.Beams*base.Taps*4*2)
	if err != nil {
		t.Fatal(err)
	}

	for _, order := range []string{"tame", "taem", "mate", "maet"} {
		g := base
		g.mics = []int{1, 2, 4, 5}
		w := &Weights{g: g, window: win}
		if !fill4(w, values, order, true, false) {
			continue
		}
		b := w.New()
		b.Mix(planeWave(0, 16*testFrame))

		t.Logf("%s  energies %v", order, b.energy)
	}
}
