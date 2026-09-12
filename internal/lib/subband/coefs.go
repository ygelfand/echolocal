package subband

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The coefficient files are one float per line with a trailing comma, C comments between them, and the
// two firmwares lay the same numbers out differently. Fire OS 5 says so in its own block comments:
//
//	// BAND 00-03   BEAM 00   COEF 3   MIC 0 (4REAL-4IMAG)
//
// Fire OS 6's file carries no comments at all. Its layout was found by asking which axis assignment
// leaves the band axis smooth, since a filter bank's weights change slowly from one band to the next:
// with the answer read off the Fire OS 5 file first to check the question was a fair one.

// axes names the four dimensions in order, outermost first: a band, e beam, t tap, m microphone.
type axes string

// readFloats parses a coefficient file and insists on the count it should hold: a file of the wrong
// length is a different tuning, and reading it as this one would point the beams at nothing.
func readFloats(path string, want int) ([]float32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make([]float32, 0, want)
	s := bufio.NewScanner(f)
	for line := 1; s.Scan(); line++ {
		text := strings.TrimSpace(s.Text())
		text = strings.TrimSuffix(text, ",")
		if text == "" || strings.HasPrefix(text, "/*") || strings.HasPrefix(text, "//") {
			continue
		}

		v, err := strconv.ParseFloat(text, 32)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %q is not a coefficient", path, line, text)
		}
		out = append(out, float32(v))
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(out) != want {
		return nil, fmt.Errorf("%s: %d coefficients, expected %d", path, len(out), want)
	}
	return out, nil
}

// readFBF fills the beamformer weights, walking the file in whatever order this firmware wrote it.
func (w *Weights) readFBF(path string) error {
	g := w.g
	values, err := readFloats(path, g.Bands*g.Beams*g.Taps*g.Inputs()*2)
	if err != nil {
		return err
	}
	w.fbf = make([]complex64, g.Bands*g.Beams*g.Taps*g.Inputs())

	// The band axis carries perGroup of them at a time, so it is walked in groups and the block that
	// lands holds that many bands of each part.
	size := map[byte]int{
		'a': g.Bands / g.perGroup,
		'e': g.Beams,
		't': g.Taps,
		'm': g.Inputs(),
	}
	o := g.order

	at := 0
	for i0 := range size[o[0]] {
		for i1 := range size[o[1]] {
			for i2 := range size[o[2]] {
				for i3 := range size[o[3]] {
					pos := map[byte]int{o[0]: i0, o[1]: i1, o[2]: i2, o[3]: i3}
					tap := pos['t']
					if g.tapDown {
						tap = g.Taps - 1 - tap
					}

					block := values[at : at+2*g.perGroup]
					at += 2 * g.perGroup

					for b := range g.perGroup {
						re, im := block[b], block[g.perGroup+b]
						if !g.split {
							re, im = block[2*b], block[2*b+1]
						}
						w.weight(pos['a']*g.perGroup+b, pos['e'], tap)[pos['m']] = complex(re, im)
					}
				}
			}
		}
	}
	return nil
}
