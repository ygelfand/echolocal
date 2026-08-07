package speaker

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ygelfand/echolocal/internal/lib/alsa"
)

// flaky underruns a few times before taking anything, and keeps what it was handed each time.
type flaky struct {
	fails int
	got   [][]byte
}

func (f *flaky) Write(p []byte) (int, error) {
	f.got = append(f.got, bytes.Clone(p))
	if f.fails > 0 {
		f.fails--
		return 0, alsa.ErrUnderrun
	}
	return len(p), nil
}

// The frames were already taken off the queue, so an underrun must put the same ones back rather than
// move on to the next period.
func TestAnUnderrunRetriesTheSameAudio(t *testing.T) {
	p := &Player{}
	to := &flaky{fails: 2}
	buf := []byte{1, 2, 3, 4}

	if err := p.send(context.Background(), to, buf); err != nil {
		t.Fatalf("send = %v", err)
	}

	if len(to.got) != 3 {
		t.Fatalf("wrote %d times, want 3", len(to.got))
	}
	for i, got := range to.got {
		if !slices.Equal(got, buf) {
			t.Errorf("attempt %d wrote %v, wanted the same %v", i, got, buf)
		}
	}
	if p.Underruns() != 2 {
		t.Errorf("underruns = %d, want 2", p.Underruns())
	}
}

func TestAWriteThatWorksIsNotCountedOrRepeated(t *testing.T) {
	p := &Player{}
	to := &flaky{}

	if err := p.send(context.Background(), to, []byte{1, 2}); err != nil {
		t.Fatalf("send = %v", err)
	}

	if len(to.got) != 1 {
		t.Errorf("wrote %d times, want 1", len(to.got))
	}
	if p.Underruns() != 0 {
		t.Errorf("underruns = %d, want 0", p.Underruns())
	}
}

// dead never recovers, which is how a retry loop becomes a spin.
type dead struct{ writes int }

func (d *dead) Write(p []byte) (int, error) {
	d.writes++
	return 0, alsa.ErrUnderrun
}

func TestRetryingStopsWhenTheContextDoes(t *testing.T) {
	p := &Player{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := p.send(ctx, &dead{}, []byte{1, 2}); !errors.Is(err, context.Canceled) {
		t.Errorf("send = %v, want context.Canceled", err)
	}
}

func TestAnythingOtherThanAnUnderrunIsReturned(t *testing.T) {
	p := &Player{}
	want := errors.New("device gone")

	err := p.send(context.Background(), writerFunc(func([]byte) (int, error) {
		return 0, want
	}), []byte{1, 2})

	if !errors.Is(err, want) {
		t.Errorf("send = %v, want %v", err, want)
	}
	if p.Underruns() != 0 {
		t.Errorf("underruns = %d, want 0", p.Underruns())
	}
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }
