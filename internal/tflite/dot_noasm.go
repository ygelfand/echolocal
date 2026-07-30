//go:build !arm64 || noasm

package tflite

// Built for anything but arm64, and for arm64 with the noasm tag — which exists so the same
// benchmarks can be run against both implementations on the hardware that matters, rather than
// against the one this happens to be compiled for.
func dot(a, b []float32) float32 { return dotGo(a, b) }
