//go:build board_crown

package mic

import (
	"slices"
	"testing"

	"github.com/ygelfand/echolocal/internal/config"
)

// beam.go steers a 7-mic ring; crown's 4 mics are not one.
func TestCrownDoesNotOfferDelayAndSum(t *testing.T) {
	if slices.Contains(Mixings(), config.MixDelaySum) {
		t.Errorf("crown offers %v, want no delay and sum", Mixings())
	}
}
