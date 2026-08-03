package vec

import (
	"math"
	"math/rand/v2"
	"strconv"
	"testing"
)

// lengths are what callers actually ask for, plus the lengths where an implementation that works on
// whole blocks gets its arithmetic wrong. The tflite convolutions reduce over 32 and 64 input
// channels, the mel filters are 512 taps, and the AEC filter is 512 or 1024. The rest are one short of
// a block, one past it, and everything below the first one.
var lengths = []int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 15, 16, 17, 19, 31, 32, 33, 63, 64, 65, 96, 127, 512, 513, 1000, 1024,
}

func random(n int, seed uint64) []float32 {
	r := rand.New(rand.NewPCG(seed, 7))
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(r.NormFloat64())
	}
	return out
}

// The two implementations group their additions differently, so they are held to agreeing about the
// value rather than about the bits.
//
// The error is measured against the size of the terms, not against the answer. A sum of random signed
// products lands near zero however large the products were, and dividing by that says an implementation
// is wildly wrong when what actually happened is that the terms cancelled: the rounding error a
// reordering causes is a property of what was added, not of what was left.
func TestDotAgreesWithTheGoImplementation(t *testing.T) {
	for _, n := range lengths {
		a, b := random(n, uint64(n)), random(n, uint64(n)+1000)

		var scale float64
		for i := range a {
			scale += math.Abs(float64(a[i]) * float64(b[i]))
		}

		want, got := dotGo(a, b), Dot(a, b)
		if d := math.Abs(float64(got-want)) / math.Max(1, scale); d > 1e-6 {
			t.Errorf("n=%d: %v against %v, off by %.3g of what was added", n, got, want, d)
		}
	}
}

// Exact cases, where reordering cannot hide a wrong answer: powers of two are exact in float32, so the
// sum is too, and every length has to land on it whatever block the implementation works in.
func TestDotExactly(t *testing.T) {
	for _, n := range lengths {
		a, b := make([]float32, n), make([]float32, n)
		for i := range a {
			a[i], b[i] = 2, float32(i%4+1)
		}

		var want float32
		for i := range a {
			want += a[i] * b[i]
		}
		if got := Dot(a, b); got != want {
			t.Errorf("n=%d: %v, want %v", n, got, want)
		}
	}
}

// A dot product of nothing is zero, and neither implementation may read the slices to find that out.
func TestDotOfEmptySlices(t *testing.T) {
	if got := Dot(nil, nil); got != 0 {
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
	Dot(make([]float32, 8), make([]float32, 4))
}

func TestAXPYAgreesWithTheGoImplementation(t *testing.T) {
	const gain = 0.375

	for _, n := range lengths {
		x := random(n, uint64(n)+2000)
		want, got := random(n, uint64(n)+3000), make([]float32, n)
		copy(got, want)

		axpyGo(want, gain, x)
		AXPY(got, gain, x)

		for i := range want {
			if d := math.Abs(float64(got[i] - want[i])); d > 1e-6 {
				t.Fatalf("n=%d index %d: %v, want %v", n, i, got[i], want[i])
			}
		}
	}
}

// Exact again: a gain of two and values that are powers of two cannot round, so every block boundary
// and the scalar tail have to be exactly right rather than nearly.
func TestAXPYExactly(t *testing.T) {
	for _, n := range lengths {
		x := make([]float32, n)
		dst := make([]float32, n)
		want := make([]float32, n)
		for i := range x {
			x[i] = float32(i%4 + 1)
			dst[i], want[i] = 8, 8+2*float32(i%4+1)
		}

		AXPY(dst, 2, x)
		for i := range want {
			if dst[i] != want[i] {
				t.Fatalf("n=%d index %d: %v, want %v", n, i, dst[i], want[i])
			}
		}
	}
}

// The block structure makes running off the end easy to get wrong, and a store past the slice would
// corrupt whatever the allocator put there rather than panicking.
func TestAXPYStaysInBounds(t *testing.T) {
	for _, n := range lengths {
		if n == 0 {
			continue
		}

		buf := make([]float32, n+16)
		for i := range buf {
			buf[i] = 99
		}

		x := make([]float32, n)
		for i := range x {
			x[i] = 1
		}

		AXPY(buf[:n], 1, x)
		for i := n; i < len(buf); i++ {
			if buf[i] != 99 {
				t.Fatalf("n=%d: wrote past the end at index %d", n, i)
			}
		}
	}
}

func TestAXPYOfEmptySlices(t *testing.T) {
	AXPY(nil, 1, nil)
}

func TestAXPYPanicsOnAShortDestination(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	AXPY(make([]float32, 4), 1, make([]float32, 8))
}

func TestSumSquaresExactly(t *testing.T) {
	for _, n := range lengths {
		a := make([]float32, n)
		for i := range a {
			a[i] = float32(i%4 + 1)
		}

		var want float32
		for _, v := range a {
			want += v * v
		}
		if got := SumSquares(a); got != want {
			t.Errorf("n=%d: %v, want %v", n, got, want)
		}
	}
}

var sink float32

func BenchmarkDot(b *testing.B) {
	for _, n := range []int{32, 64, 512, 1024} {
		x, y := make([]float32, n), make([]float32, n)
		for i := range x {
			x[i], y[i] = float32(i)*0.001, float32(n-i)*0.001
		}

		b.Run("asm/"+strconv.Itoa(n), func(b *testing.B) {
			b.SetBytes(int64(n * 4))
			for range b.N {
				sink = Dot(x, y)
			}
		})
		b.Run("go/"+strconv.Itoa(n), func(b *testing.B) {
			b.SetBytes(int64(n * 4))
			for range b.N {
				sink = dotGo(x, y)
			}
		})
	}
}

func BenchmarkAXPY(b *testing.B) {
	for _, n := range []int{32, 64, 512, 1024} {
		x, dst := make([]float32, n), make([]float32, n)
		for i := range x {
			x[i], dst[i] = float32(i)*0.001, float32(n-i)*0.001
		}

		b.Run("asm/"+strconv.Itoa(n), func(b *testing.B) {
			b.SetBytes(int64(n * 4))
			for range b.N {
				AXPY(dst, 1e-9, x)
			}
		})
		b.Run("go/"+strconv.Itoa(n), func(b *testing.B) {
			b.SetBytes(int64(n * 4))
			for range b.N {
				axpyGo(dst, 1e-9, x)
			}
		})
	}
}
