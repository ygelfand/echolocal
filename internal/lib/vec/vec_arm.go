//go:build arm && !noasm

package vec

// An empty slice never reaches the assembly, because taking the address of the first element of one
// panics. The reslice is what the Go implementations do too, and is what raises the same panic when
// the second slice is short.

// Dot is the sum of a[i]*b[i].
func Dot(a, b []float32) float32 {
	if len(a) == 0 {
		return 0
	}
	b = b[:len(a)]
	return dotVFP(&a[0], &b[0], len(a))
}

// AXPY adds gain*x[i] to dst[i]. The echo canceller updates a thousand-tap filter with it on every
// frame it runs.
func AXPY(dst []float32, gain float32, x []float32) {
	if len(x) == 0 {
		return
	}
	dst = dst[:len(x)]
	axpyVFP(&dst[0], &x[0], len(x), gain)
}

//go:noescape
func dotVFP(a, b *float32, n int) float32

//go:noescape
func axpyVFP(dst, x *float32, n int, gain float32)
