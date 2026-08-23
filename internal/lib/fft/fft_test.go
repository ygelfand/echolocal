package fft

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestRoundTrips(t *testing.T) {
	const n = 128

	f := New(n)
	want := make([]complex64, n)
	for i := range want {
		want[i] = complex(rand.Float32()*2-1, rand.Float32()*2-1)
	}

	got := append([]complex64(nil), want...)
	f.Forward(got)
	f.Inverse(got)

	for i := range want {
		if diff := abs(got[i] - want[i]); diff > 1e-4 {
			t.Fatalf("sample %d came back %v, sent %v", i, got[i], want[i])
		}
	}
}

// The transform has to agree with the definition, not just with its own inverse: a wrong twiddle
// sign inverts cleanly and still puts the bands in the wrong place.
func TestMatchesTheDefinition(t *testing.T) {
	const n = 16

	in := make([]complex64, n)
	for i := range in {
		in[i] = complex(rand.Float32()*2-1, rand.Float32()*2-1)
	}

	got := append([]complex64(nil), in...)
	New(n).Forward(got)

	for k := range n {
		var want complex128
		for m := range n {
			a := -2 * math.Pi * float64(k) * float64(m) / float64(n)
			want += complex128(complex(float64(real(in[m])), float64(imag(in[m])))) *
				complex(math.Cos(a), math.Sin(a))
		}
		if diff := abs(got[k] - complex(float32(real(want)), float32(imag(want)))); diff > 1e-4 {
			t.Fatalf("bin %d = %v, want %v", k, got[k], want)
		}
	}
}

func TestSizeMustBeAPowerOfTwo(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a size of 100 was accepted")
		}
	}()
	New(100)
}

func abs(c complex64) float64 { return math.Hypot(float64(real(c)), float64(imag(c))) }
