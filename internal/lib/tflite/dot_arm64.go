//go:build arm64 && !noasm

package tflite

// dot on arm64 goes through NEON, which is the whole reason echod fits in its time budget on this
// hardware: the device is a Cortex-A53 at 1.3 GHz and the embedding model has to run inside one 80 ms
// step, on a core that is also carrying the microphones and the ring.
//
// An empty slice never reaches the assembly, because taking the address of the first element of one
// panics. The reslice of b is what the Go implementation does too, and is what raises the same panic
// when b is short.
func dot(a, b []float32) float32 {
	if len(a) == 0 {
		return 0
	}
	b = b[:len(a)]
	return dotNEON(&a[0], &b[0], len(a))
}

//go:noescape
func dotNEON(a, b *float32, n int) float32
