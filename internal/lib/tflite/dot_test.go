package tflite

import (
	"math"
	"math/rand"
	"testing"
)

// dotLengths are what the models actually ask for: the embedding convolutions reduce over their input
// channels, which are 32 and 64, and the mel model's filters are 512 taps. The rest are the lengths
// where an implementation that works on whole vectors gets its arithmetic wrong — one short of a
// block, one past it, and everything below the first one.
var dotLengths = []int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 15, 16, 17, 19, 31, 32, 33, 63, 64, 65, 96, 127, 512, 513, 1000,
}

// The two implementations group their additions differently, so they are held to agreeing about the
// value rather than about the bits.
//
// The error is measured against the size of the terms, not against the answer. A sum of random signed
// products lands near zero however large the products were, and dividing by that says an implementation
// is wildly wrong when what actually happened is that the terms cancelled: the rounding error a
// reordering causes is a property of what was added, not of what was left.
func TestDotAgreesWithTheGoImplementation(t *testing.T) {
	r := rand.New(rand.NewSource(1))

	for _, n := range dotLengths {
		a, b := make([]float32, n), make([]float32, n)
		var scale float64
		for i := range a {
			a[i] = float32(r.NormFloat64())
			b[i] = float32(r.NormFloat64())
			scale += math.Abs(float64(a[i]) * float64(b[i]))
		}

		want, got := dotGo(a, b), dot(a, b)
		if d := math.Abs(float64(got-want)) / math.Max(1, scale); d > 1e-6 {
			t.Errorf("n=%d: %v against %v, off by %.3g of what was added", n, got, want, d)
		}
	}
}

// A dot product of nothing is zero, and neither implementation may read the slices to find that out.
func TestDotOfEmptySlices(t *testing.T) {
	if got := dot(nil, nil); got != 0 {
		t.Errorf("dot of nothing is %v", got)
	}
}

// b shorter than a is a bug in the caller, and both implementations have to say so rather than reading
// past the end of it.
func TestDotPanicsOnAShortSecondSlice(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	dot(make([]float32, 8), make([]float32, 4))
}

// Exact cases, where reordering cannot hide a wrong answer: powers of two are exact in float32, so the
// sum is too, and every length has to land on it whatever block the implementation works in.
func TestDotExactly(t *testing.T) {
	for _, n := range dotLengths {
		a, b := make([]float32, n), make([]float32, n)
		for i := range a {
			a[i], b[i] = 2, float32(i%4+1)
		}

		var want float32
		for i := range a {
			want += a[i] * b[i]
		}
		if got := dot(a, b); got != want {
			t.Errorf("n=%d: %v, want %v", n, got, want)
		}
	}
}

func BenchmarkDot(b *testing.B) {
	for _, n := range []int{32, 64, 512} {
		x, y := make([]float32, n), make([]float32, n)
		for i := range x {
			x[i], y[i] = float32(i)*0.001, float32(n-i)*0.001
		}

		b.Run("asm/"+itoa(n), func(b *testing.B) {
			b.SetBytes(int64(n * 4))
			for range b.N {
				sink = dot(x, y)
			}
		})
		b.Run("go/"+itoa(n), func(b *testing.B) {
			b.SetBytes(int64(n * 4))
			for range b.N {
				sink = dotGo(x, y)
			}
		})
	}
}

var sink float32

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}
