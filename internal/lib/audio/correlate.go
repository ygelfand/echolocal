package audio

import (
	"fmt"
	"io"
	"math"
)

// Channels splits an interleaved S24_3LE buffer into per-channel float slices.
func Channels(raw []byte, chans int) [][]float64 {
	frameBytes := chans * 3
	frames := len(raw) / frameBytes

	out := make([][]float64, chans)
	for c := range out {
		out[c] = make([]float64, frames)
	}
	for f := 0; f < frames; f++ {
		off := f * frameBytes
		for c := 0; c < chans; c++ {
			out[c][f] = float64(DecodeS24LE3(raw[off+c*3:]))
		}
	}
	return out
}

// Correlation is the Pearson coefficient between two channels at zero lag.
func Correlation(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}

	var ma, mb float64
	for i := 0; i < n; i++ {
		ma += a[i]
		mb += b[i]
	}
	ma /= float64(n)
	mb /= float64(n)

	var num, da, db float64
	for i := 0; i < n; i++ {
		x, y := a[i]-ma, b[i]-mb
		num += x * y
		da += x * x
		db += y * y
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
}

// CorrelationMatrix reports pairwise correlation between all channels.
//
// Interpreting a silent-room capture:
//
//   - independent microphones correlate near zero, because each preamp's noise is its own
//   - a channel that is a hardware sum of others correlates strongly with all of them
//   - a true center mic correlates about equally with every perimeter mic, while a
//     perimeter mic correlates most with its immediate neighbours
func CorrelationMatrix(w io.Writer, ch [][]float64) {
	n := len(ch)

	fmt.Fprintf(w, "\n%4s", "")
	for c := 0; c < n; c++ {
		fmt.Fprintf(w, "%7d", c)
	}
	fmt.Fprintln(w)

	for i := 0; i < n; i++ {
		fmt.Fprintf(w, "%4d", i)
		for j := 0; j < n; j++ {
			fmt.Fprintf(w, "%7.3f", Correlation(ch[i], ch[j]))
		}
		fmt.Fprintln(w)
	}
}

// MeanAbsCorrelation is a channel's average |correlation| with all others. A hardware mix
// stands out as an outlier here.
func MeanAbsCorrelation(ch [][]float64, i int) float64 {
	if len(ch) < 2 {
		return 0
	}
	var sum float64
	for j := range ch {
		if j == i {
			continue
		}
		sum += math.Abs(Correlation(ch[i], ch[j]))
	}
	return sum / float64(len(ch)-1)
}

// Topology summarises array structure from a correlation matrix, without assuming geometry.
//
// CenterSpread is the max-min range of a channel's correlations to the other perimeter
// mics. A center mic is equidistant from all of them, so its spread is small; a perimeter
// mic favours neighbours and so spreads wider.
//
// ByDistance is mean correlation grouped by separation around the ring: 1 = adjacent,
// 2 = two apart, 3 = opposite. For a ring of 6 at radius r the physical separations are
// r, r*sqrt(3) and 2r, so correlation must fall monotonically as distance grows for any
// direction of arrival. If it does not, the assumed ring order is wrong.
type Topology struct {
	Center       int
	CenterSpread float64
	Spreads      []float64
	ByDistance   [4]float64
}

// AnalyzeTopology treats perimeter as the assumed ring order and center as the candidate
// center channel, and reports how well the data supports that arrangement.
func AnalyzeTopology(ch [][]float64, perimeter []int, center int) Topology {
	t := Topology{Center: center}

	spread := func(i int) float64 {
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, j := range perimeter {
			if j == i {
				continue
			}
			v := Correlation(ch[i], ch[j])
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
		return hi - lo
	}

	t.CenterSpread = spread(center)
	for _, i := range perimeter {
		t.Spreads = append(t.Spreads, spread(i))
	}

	n := len(perimeter)
	var sums [4]float64
	var counts [4]int
	for a := 0; a < n; a++ {
		for b := a + 1; b < n; b++ {
			d := b - a
			if d > n/2 {
				d = n - d
			}
			sums[d] += Correlation(ch[perimeter[a]], ch[perimeter[b]])
			counts[d]++
		}
	}
	for d := 1; d <= 3; d++ {
		if counts[d] > 0 {
			t.ByDistance[d] = sums[d] / float64(counts[d])
		}
	}
	return t
}

// Monotonic reports whether correlation falls as ring distance grows, which is what
// physical separation requires.
func (t Topology) Monotonic() bool {
	return t.ByDistance[1] > t.ByDistance[2] && t.ByDistance[2] > t.ByDistance[3]
}

// SpeedOfSound in air, m/s, near room temperature.
const SpeedOfSound = 343.0

// LoudestWindow returns the start index of the highest-energy window of n samples in ref.
//
// Transient sources such as taps leave most of a recording as silence, which dilutes a
// whole-signal correlation until the noise floor dominates the result. Restricting the
// analysis to the transient keeps the direction estimate meaningful.
func LoudestWindow(ref []float64, n int) int {
	if n <= 0 || n >= len(ref) {
		return 0
	}
	var sum float64
	for i := 0; i < n; i++ {
		sum += ref[i] * ref[i]
	}
	best, bestSum := 0, sum
	for i := n; i < len(ref); i++ {
		sum += ref[i]*ref[i] - ref[i-n]*ref[i-n]
		if sum > bestSum {
			best, bestSum = i-n+1, sum
		}
	}
	return best
}

// SliceAll takes the same window from every channel, preserving alignment between them.
func SliceAll(ch [][]float64, start, n int) [][]float64 {
	out := make([][]float64, len(ch))
	for i, c := range ch {
		end := start + n
		if end > len(c) {
			end = len(c)
		}
		if start >= end {
			out[i] = c
			continue
		}
		out[i] = c[start:end]
	}
	return out
}

// SubSampleLag finds the cross-correlation peak between a and b to sub-sample precision by
// fitting a parabola through the peak and its neighbours.
//
// Sign convention: the returned lag is the raw correlation offset. If a is a delayed copy
// of b the peak lands at a *negative* offset, so a more negative value means a arrived
// LATER. Use ArrivalOffset for a value that reads as an arrival time.
//
// This matters on this array: 36 mm of separation is ~105 us, only 1.7 samples at 16 kHz,
// so integer lag quantises the entire useful range into three or four values.
func SubSampleLag(a, b []float64, maxLag int) (float64, float64) {
	corr := make([]float64, 2*maxLag+1)
	for i := range corr {
		lag := i - maxLag
		var x, y []float64
		switch {
		case lag < 0:
			x, y = a[-lag:], b[:len(b)+lag]
		case lag > 0:
			x, y = a[:len(a)-lag], b[lag:]
		default:
			x, y = a, b
		}
		corr[i] = Correlation(x, y)
	}

	peak := 0
	for i, v := range corr {
		if v > corr[peak] {
			peak = i
		}
	}
	lag := float64(peak - maxLag)

	// Parabolic interpolation needs a neighbour on each side.
	if peak > 0 && peak < len(corr)-1 {
		l, c, r := corr[peak-1], corr[peak], corr[peak+1]
		if d := l - 2*c + r; d != 0 {
			lag += 0.5 * (l - r) / d
		}
	}
	return lag, corr[peak]
}

// ArrivalOffset returns when a heard the source relative to b, in samples. Negative means a
// heard it first. This is the negation of the raw correlation lag, and is the form to use
// for direction finding.
func ArrivalOffset(a, b []float64, maxLag int) (float64, float64) {
	lag, corr := SubSampleLag(a, b, maxLag)
	return -lag, corr
}

// FitBearing estimates the source direction from all perimeter arrival offsets at once,
// rather than trusting whichever single mic happens to read loudest.
//
// For a distant source at bearing t, a mic at ring angle p hears it at offset
// -A*cos(t - p), where A is the ring radius expressed in samples. Projecting the offsets
// onto sin and cos of the mic angles recovers t and A by least squares.
//
// The returned bearing is relative to mic 0, in degrees, increasing in the same direction
// as mic index. Absolute orientation needs a source at a known physical bearing.
//
// Amplitude is a quality signal: for a source in the plane of the ring it should approach
// r/c in samples (about 1.68 at 16 kHz with r=36 mm). A materially smaller value means the
// source was elevated, since only the in-plane component produces inter-mic delay.
func FitBearing(offsets []float64) (bearingDeg, amplitude float64) {
	n := len(offsets)
	if n == 0 {
		return 0, 0
	}
	var sc, ss float64
	for i, off := range offsets {
		a := 2 * math.Pi * float64(i) / float64(n)
		sc += off * math.Cos(a)
		ss += off * math.Sin(a)
	}
	// Sum of cos^2 over evenly spaced angles is n/2, and the model carries a minus sign.
	cx, cy := -sc/(float64(n)/2), -ss/(float64(n)/2)

	deg := math.Atan2(cy, cx) * 180 / math.Pi
	if deg < 0 {
		deg += 360
	}
	return deg, math.Hypot(cx, cy)
}

// InPlaneLag is the expected arrival offset in samples between the ring edge and its center
// for a source in the plane of the array.
func InPlaneLag(radiusMM float64, rate int) float64 {
	return radiusMM / 1000 / SpeedOfSound * float64(rate)
}

// LagToDistance converts a lag in samples to a path-length difference in millimetres.
func LagToDistance(lagSamples float64, rate int) float64 {
	return lagSamples / float64(rate) * SpeedOfSound * 1000
}

// BestLag cross-correlates a against b over +/-maxLag samples and returns the lag with the
// highest correlation, plus that correlation. Positive means a lags b.
//
// The array is small: 36 mm spacing is ~210 us, only 3.4 samples at 16 kHz, so integer lag
// alone is coarse and this is a starting point rather than a geometry solution.
func BestLag(a, b []float64, maxLag int) (int, float64) {
	best, bestVal := 0, math.Inf(-1)
	for lag := -maxLag; lag <= maxLag; lag++ {
		var x, y []float64
		switch {
		case lag < 0:
			x, y = a[-lag:], b[:len(b)+lag]
		case lag > 0:
			x, y = a[:len(a)-lag], b[lag:]
		default:
			x, y = a, b
		}
		if v := Correlation(x, y); v > bestVal {
			best, bestVal = lag, v
		}
	}
	return best, bestVal
}
