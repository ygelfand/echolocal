package speaker

import (
	"encoding/binary"
	"testing"

	"github.com/ygelfand/echolocal/internal/config"
)

func TestRescalingWhatIsQueuedNeverPlaysSilence(t *testing.T) {
	const periods = 200

	p := newTestPlayer()
	queued := make([]int16, period*Channels*periods)
	for i := range queued {
		queued[i] = 1000
	}
	p.bus.Push(config.StreamMedia, queued)

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
