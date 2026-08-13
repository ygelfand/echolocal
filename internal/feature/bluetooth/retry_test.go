package bluetooth

import (
	"testing"
	"time"
)

func TestSettleRadioRetriesWithBoundedBackoff(t *testing.T) {
	wanted := make(chan struct{}, 1)
	ticks := make(chan time.Time)
	delays := make(chan time.Duration)
	attempts := make(chan int)
	exited := make(chan struct{})
	count := 0

	go func() {
		settleRadio(wanted, func() bool {
			count++
			attempts <- count
			return count == 8
		}, func(delay time.Duration) <-chan time.Time {
			delays <- delay
			return ticks
		})
		close(exited)
	}()

	wanted <- struct{}{}
	wantDelays := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	for attempt, wantDelay := range wantDelays {
		if got := receive(t, attempts); got != attempt+1 {
			t.Fatalf("attempt = %d, want %d", got, attempt+1)
		}
		if got := receive(t, delays); got != wantDelay {
			t.Fatalf("delay = %s, want %s", got, wantDelay)
		}
		ticks <- time.Now()
	}
	if got := receive(t, attempts); got != 8 {
		t.Fatalf("attempt = %d, want 8", got)
	}

	close(wanted)
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("settler did not exit")
	}
}

func TestSettleRadioNewRequestResetsBackoff(t *testing.T) {
	wanted := make(chan struct{}, 1)
	ticks := make(chan time.Time)
	delays := make(chan time.Duration)
	attempts := make(chan int)
	exited := make(chan struct{})
	count := 0

	go func() {
		settleRadio(wanted, func() bool {
			count++
			attempts <- count
			return count == 3
		}, func(delay time.Duration) <-chan time.Time {
			delays <- delay
			return ticks
		})
		close(exited)
	}()

	wanted <- struct{}{}
	if got := receive(t, attempts); got != 1 {
		t.Fatalf("attempt = %d, want 1", got)
	}
	if got := receive(t, delays); got != radioRetryInitial {
		t.Fatalf("delay = %s, want %s", got, radioRetryInitial)
	}

	wanted <- struct{}{}
	if got := receive(t, attempts); got != 2 {
		t.Fatalf("attempt = %d, want 2", got)
	}
	if got := receive(t, delays); got != radioRetryInitial {
		t.Fatalf("delay = %s, want reset to %s", got, radioRetryInitial)
	}

	ticks <- time.Now()
	if got := receive(t, attempts); got != 3 {
		t.Fatalf("attempt = %d, want 3", got)
	}

	close(wanted)
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("settler did not exit")
	}
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}
