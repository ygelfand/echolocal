package subband

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The coefficient files are one float per line with a trailing comma, C comments between them. The
// block comments name what follows, which is how the layout below was read off them:
//
//	// BAND 00-03   BEAM 00   COEF 3   MIC 0 (4REAL-4IMAG)
//
// so the order is band group, beam, tap counting down, microphone, and each block is the group's
// four real parts then its four imaginary parts.
const bandsPerGroup = 4

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

// readFBF fills the beamformer weights.
func (w *Weights) readFBF(path string) error {
	const blocks = Bands / bandsPerGroup * Beams * Taps * Inputs
	values, err := readFloats(path, blocks*2*bandsPerGroup)
	if err != nil {
		return err
	}

	at := 0
	for group := 0; group < Bands/bandsPerGroup; group++ {
		for beam := range Beams {
			// The file counts taps down, so the first block of a beam is the oldest.
			for tap := Taps - 1; tap >= 0; tap-- {
				for m := range Inputs {
					block := values[at : at+2*bandsPerGroup]
					at += 2 * bandsPerGroup

					for b := range bandsPerGroup {
						re := block[b]
						im := block[bandsPerGroup+b]
						w.fbf[group*bandsPerGroup+b][beam][tap][m] = complex(re, im)
					}
				}
			}
		}
	}
	return nil
}
