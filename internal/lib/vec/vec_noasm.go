//go:build !arm64 || noasm

package vec

// Built for anything but arm64, and for arm64 with the noasm tag — which exists so the same benchmarks
// can be run against both implementations on the hardware that matters, rather than against the one
// this happens to be compiled for.

// Dot is the sum of a[i]*b[i].
func Dot(a, b []float32) float32 { return dotGo(a, b) }

// AXPY adds gain*x[i] to dst[i].
func AXPY(dst []float32, gain float32, x []float32) { axpyGo(dst, gain, x) }
