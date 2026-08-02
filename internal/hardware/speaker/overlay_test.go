package speaker

import (
	"math"
	"slices"
	"testing"
)

// Mixing is what makes a beep during a reply immediate rather than arriving after it: what is queued
// is a reply's cushion, or when it came as a file, all of it.
func TestMixSumsIntoWhatIsQueued(t *testing.T) {
	got := mix([]int16{100, 200, 300}, []int16{10, 20, 30})

	if want := []int16{110, 220, 330}; !slices.Equal(got, want) {
		t.Errorf("mix = %v, want %v", got, want)
	}
}

// A tone longer than what is queued keeps sounding past it.
func TestMixExtendsPastTheQueue(t *testing.T) {
	got := mix([]int16{100}, []int16{10, 20, 30})

	if want := []int16{110, 20, 30}; !slices.Equal(got, want) {
		t.Errorf("mix = %v, want %v", got, want)
	}
}

// Two loud things at once must not wrap, which would replace the beep with a crack.
func TestMixClampsInsteadOfWrapping(t *testing.T) {
	got := mix([]int16{math.MaxInt16 - 10, math.MinInt16 + 10}, []int16{1000, -1000})

	if want := []int16{math.MaxInt16, math.MinInt16}; !slices.Equal(got, want) {
		t.Errorf("mix = %v, want %v", got, want)
	}
}

func TestMixIntoNothing(t *testing.T) {
	got := mix(nil, []int16{10, 20})

	if want := []int16{10, 20}; !slices.Equal(got, want) {
		t.Errorf("mix = %v, want %v", got, want)
	}
}
