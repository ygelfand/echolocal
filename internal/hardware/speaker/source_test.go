package speaker

import (
	"encoding/binary"
	"math"
	"testing"
)

// at records where it was asked to render, which is the whole point of a Source: the same audio has to
// land at the same output frame on every device in the group.
type recorder struct {
	at   []uint64
	fill int16
}

func (r *recorder) Render(at uint64, out []int16) {
	r.at = append(r.at, at)
	for i := range out {
		out[i] = r.fill
	}
}

func played(t *testing.T, p *Player) []int16 {
	t.Helper()

	buf := make([]byte, period*Channels*2)
	p.fill(buf)

	got := make([]int16, period*Channels)
	for i := range got {
		got[i] = int16(binary.LittleEndian.Uint16(buf[i*2:]))
	}
	return got
}

func newTestPlayer() *Player {
	p := &Player{}
	p.volume.Store(math.Float32bits(1))
	return p
}

func TestNothingAttachedPlaysTheQueueAlone(t *testing.T) {
	p := newTestPlayer()
	p.pending = []int16{100, 200, 300}

	got := played(t, p)

	if got[0] != 100 || got[1] != 200 || got[2] != 300 {
		t.Errorf("queue = %v, want 100 200 300", got[:3])
	}
	if got[3] != 0 {
		t.Errorf("past the queue = %d, want silence", got[3])
	}
}

// A reply is queued while a room stream is rendered, and both have to be audible.
func TestASourceIsSummedWithTheQueue(t *testing.T) {
	p := newTestPlayer()
	p.pending = []int16{100, 200}
	p.Attach(&recorder{fill: 10})

	got := played(t, p)

	if got[0] != 110 || got[1] != 210 {
		t.Errorf("mixed = %v, want 110 210", got[:2])
	}
	if got[2] != 10 {
		t.Errorf("past the queue = %d, want the source alone", got[2])
	}
}

func TestSummingTwoLoudThingsClampsInsteadOfWrapping(t *testing.T) {
	p := newTestPlayer()
	p.pending = []int16{math.MaxInt16 - 10}
	p.Attach(&recorder{fill: 1000})

	if got := played(t, p)[0]; got != math.MaxInt16 {
		t.Errorf("mixed = %d, want %d", got, math.MaxInt16)
	}
}

// Where audio lands must follow from the frame index, and the index only moves on a completed write.
func TestTheSourceIsAskedForTheFramesAboutToBePlayed(t *testing.T) {
	p := newTestPlayer()
	rec := &recorder{}
	p.Attach(rec)

	played(t, p)
	p.written.Add(period)
	played(t, p)

	want := []uint64{0, period}
	if len(rec.at) != 2 || rec.at[0] != want[0] || rec.at[1] != want[1] {
		t.Errorf("asked at %v, want %v", rec.at, want)
	}
}

func TestDetachingStopsTheSource(t *testing.T) {
	p := newTestPlayer()
	p.Attach(&recorder{fill: 10})
	p.Attach(nil)

	if got := played(t, p)[0]; got != 0 {
		t.Errorf("detached = %d, want silence", got)
	}
}
