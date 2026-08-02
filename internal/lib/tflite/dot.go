package tflite

// dot is the inner product the interpreter spends its time in. Every convolution reduces to it — the
// embedding model calls it once per output channel across twenty layers, and the mel model's 512-tap
// filters call it once per output sample — so it is the one function worth writing twice.
//
// The implementations do not agree bit for bit and are not meant to: they group the additions
// differently, and float32 addition is not associative. What holds them together is the reference
// tests, which compare whole model outputs against fixtures produced by the reference implementation
// and allow only the difference accumulation order can make.

// dotGo is the portable implementation, and the one every architecture but arm64 uses.
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
