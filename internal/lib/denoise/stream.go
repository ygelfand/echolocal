package denoise

// Stream is the filter as the microphone wants it: frames in, the same frames out, filtered. It is
// not safe for concurrent use.
//
// The estimator reads overlapping windows and returns half a window at a time, so this holds the
// samples in between. The output lags the input by one window, which everything downstream sees as
// the same audio arriving a moment later rather than as a different shape.
type Stream struct {
	f      *Filter
	window []float64
	hop    []float64

	pending []float64
	ready   []float64
}

func NewStream(rate int) *Stream {
	f := New(rate)
	s := &Stream{
		f:      f,
		window: make([]float64, f.Frame()),
		hop:    make([]float64, f.Hop()),
	}
	s.prime()
	return s
}

func (s *Stream) prime() {
	s.pending = s.pending[:0]
	s.ready = append(s.ready[:0], make([]float64, s.f.Frame())...)
}

// Forget drops what the filter learned about the room.
func (s *Stream) Forget() {
	s.f.Forget()
	s.prime()
}

// Apply filters a frame in place.
func (s *Stream) Apply(frame []int16) {
	for _, v := range frame {
		s.pending = append(s.pending, float64(v))
	}

	for len(s.pending) >= s.f.Frame() {
		copy(s.window, s.pending[:s.f.Frame()])
		s.f.Push(s.window, s.hop)
		s.ready = append(s.ready, s.hop...)
		s.pending = append(s.pending[:0], s.pending[s.f.Hop():]...)
	}

	for i := range frame {
		if i >= len(s.ready) {
			frame[i] = 0
			continue
		}
		frame[i] = clampSample(s.ready[i])
	}
	if n := min(len(frame), len(s.ready)); n > 0 {
		s.ready = append(s.ready[:0], s.ready[n:]...)
	}
}

func clampSample(v float64) int16 {
	switch {
	case v > 32767:
		return 32767
	case v < -32768:
		return -32768
	}
	return int16(v)
}
