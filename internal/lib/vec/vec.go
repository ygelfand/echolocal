// Package vec is the handful of loops over float32 slices that this device spends its time in, with
// hand-written arm64 assembly where it pays and portable Go everywhere else.
//
// The target is a Cortex-A53: two-wide and in-order, so nothing is reordered on the chip's behalf and
// a load followed immediately by its use stalls. That is why these are written by hand rather than
// taken from a library — the ones available are tuned for cores that reorder, and measured slower here
// by 1.4x on Dot and 1.8x on AXPY.
//
// The implementations do not agree bit for bit and are not meant to: they group the additions
// differently, and float32 addition is not associative.
package vec

// dotGo is the portable inner product.
//
// Four running sums rather than one because a single accumulator serialises: each addition would wait
// on the previous one to land, where four independent chains keep the pipeline fed. The tail is
// whatever is left when the length is not a multiple of four.
func dotGo(a, b []float32) float32 {
	b = b[:len(a)]
	var s0, s1, s2, s3 float32
	i := 0
	for ; i+4 <= len(a); i += 4 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
	}
	s := s0 + s1 + s2 + s3
	for ; i < len(a); i++ {
		s += a[i] * b[i]
	}
	return s
}

func axpyGo(dst []float32, gain float32, x []float32) {
	dst = dst[:len(x)]
	for i, v := range x {
		dst[i] += gain * v
	}
}

// SumSquares is the sum of a[i]*a[i]. Portable only: the callers reach for it far less often than Dot
// and AXPY, so it has never been worth assembly.
func SumSquares(a []float32) float32 {
	var s0, s1, s2, s3 float32
	i := 0
	for ; i+4 <= len(a); i += 4 {
		s0 += a[i] * a[i]
		s1 += a[i+1] * a[i+1]
		s2 += a[i+2] * a[i+2]
		s3 += a[i+3] * a[i+3]
	}
	s := s0 + s1 + s2 + s3
	for ; i < len(a); i++ {
		s += a[i] * a[i]
	}
	return s
}
