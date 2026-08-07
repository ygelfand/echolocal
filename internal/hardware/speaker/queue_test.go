package speaker

import (
	"encoding/binary"
	"math"
	"testing"
)

// Ducking must not be heard as a gap. Rescaling the queue by emptying it and putting it back leaves it
// empty in between, and a period that lands in that window is written out as silence.
func TestRescalingWhatIsQueuedNeverPlaysSilence(t *testing.T) {
	const periods = 200

	p := &Player{}
	p.volume.Store(math.Float32bits(1))
	p.pending = make([]int16, period*Channels*periods)
	for i := range p.pending {
		p.pending[i] = 1000
	}

	ducked := make(chan struct{})
	go func() {
		defer close(ducked)
		for range periods {
			p.Adjust(func(queued []int16) {
				for i := range queued {
					queued[i] = 500
				}
			})
		}
	}()

	buf := make([]byte, period*Channels*2)
	for range periods / 2 {
		p.fill(buf)
		for at := 0; at < len(buf); at += 2 {
			if int16(binary.LittleEndian.Uint16(buf[at:])) == 0 {
				t.Fatal("silence played while audio was queued")
			}
		}
	}
	<-ducked
}
