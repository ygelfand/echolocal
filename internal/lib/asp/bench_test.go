package asp

import (
	"math/rand"
	"testing"
)

func BenchmarkChain(b *testing.B) {
	h := make([]float32, defaultTaps)
	h[0] = 1
	c, err := stepped(h).Chain(1024)
	if err != nil {
		b.Fatal(err)
	}
	x := make([]float32, 1024)
	r := rand.New(rand.NewSource(1))
	b.SetBytes(int64(len(x)))
	b.ResetTimer()
	for b.Loop() {
		for i := range x {
			x[i] = r.Float32()*1.6 - 0.8
		}
		c.Process(x, 0.2)
	}
}
