package mic

import (
	"sync"
	"time"
)

// History is how much recent audio a turn sends before its live frames. Detection can only fire
// once the wake word has been spoken, and speech runs straight on into the request, so without this
// the first syllables of "turn on the kitchen light" are gone before anything is streaming.
//
// It is a compromise in both directions: too little and the first word of the request is missing,
// too much and the tail of the wake word is transcribed as part of the request, which can stop it
// matching an intent.
const History = 250 * time.Millisecond

// The ring holds a second, whatever is being sent of it.
const historySamples = Rate

// history is a ring of the most recently captured mono samples.
type history struct {
	mu     sync.Mutex
	buf    [historySamples]int16
	at     int
	filled bool
}

func (h *history) add(frame []int16) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, s := range frame {
		h.buf[h.at] = s
		h.at++
		if h.at == len(h.buf) {
			h.at, h.filled = 0, true
		}
	}
}

// recent returns up to d of the most recent audio, oldest first.
func (h *history) recent(d time.Duration) []int16 {
	want := int(d/time.Millisecond) * Rate / 1000

	h.mu.Lock()
	defer h.mu.Unlock()

	have := len(h.buf)
	if !h.filled {
		have = h.at
	}
	want = min(want, have)

	out := make([]int16, 0, want)
	// Walk back from the write position, wrapping, then hand it over oldest first.
	start := h.at - want
	if start < 0 {
		start += len(h.buf)
	}
	for i := range want {
		out = append(out, h.buf[(start+i)%len(h.buf)])
	}
	return out
}

func (s *Source) remember(frame []int16) { s.history.add(frame) }

// Recent returns up to d of the audio captured before now, oldest first. A turn sends this ahead of
// its live frames so the speech that ran on from the wake word is not lost.
func (s *Source) Recent(d time.Duration) []int16 { return s.history.recent(d) }
