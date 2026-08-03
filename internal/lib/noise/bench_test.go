package noise

import "testing"

// BenchmarkSounds reports what each costs as a share of a core: these run for hours next to the wake
// word runtime, so the number that matters is generated seconds per second, not nanoseconds.
func BenchmarkSounds(b *testing.B) {
	for _, name := range Names() {
		b.Run(name, func(b *testing.B) {
			fill := New(name, rate)
			buf := make([]float32, blockSize)

			b.ResetTimer()
			for range b.N {
				fill(buf)
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*blockSize)*rate/1e7,
				"%-of-a-core")
		})
	}
}
