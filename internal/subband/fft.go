package subband

import "math"

// fft is an in-place radix-2 transform of one fixed size, with its twiddles and bit-reversal built
// once. Nothing else in the tree needs an FFT, so this is only what the filter bank asks for.
type fft struct {
	n       int
	rev     []int
	twiddle []complex64 // e^-2πik/n for k < n/2
}

func newFFT(n int) *fft {
	if n&(n-1) != 0 {
		panic("subband: fft size is not a power of two")
	}

	f := &fft{n: n, rev: make([]int, n), twiddle: make([]complex64, n/2)}
	for i := range f.twiddle {
		a := -2 * math.Pi * float64(i) / float64(n)
		f.twiddle[i] = complex(float32(math.Cos(a)), float32(math.Sin(a)))
	}

	bits := 0
	for 1<<bits < n {
		bits++
	}
	for i := range f.rev {
		r := 0
		for b := 0; b < bits; b++ {
			r |= (i >> b & 1) << (bits - 1 - b)
		}
		f.rev[i] = r
	}
	return f
}

// forward transforms x in place.
func (f *fft) forward(x []complex64) {
	for i, r := range f.rev {
		if i < r {
			x[i], x[r] = x[r], x[i]
		}
	}

	for size := 2; size <= f.n; size <<= 1 {
		step := f.n / size
		half := size / 2
		for start := 0; start < f.n; start += size {
			for k := range half {
				a := x[start+k]
				b := x[start+k+half] * f.twiddle[k*step]
				x[start+k] = a + b
				x[start+k+half] = a - b
			}
		}
	}
}

// inverse transforms x in place, scaled so that inverse(forward(x)) is x.
func (f *fft) inverse(x []complex64) {
	for i := range x {
		x[i] = conj(x[i])
	}
	f.forward(x)

	s := complex(float32(1)/float32(f.n), 0)
	for i := range x {
		x[i] = conj(x[i]) * s
	}
}

func conj(c complex64) complex64 { return complex(real(c), -imag(c)) }
