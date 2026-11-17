//go:build board_checkers

package mic

import (
	"slices"
	"testing"

	"github.com/ygelfand/echolocal/internal/config"
)

// beam.go steers a 7-mic ring; checkers' 2 mics are not one.
func TestCheckersDoesNotOfferDelayAndSum(t *testing.T) {
	if slices.Contains(Mixings(), config.MixDelaySum) {
		t.Errorf("checkers offers %v, want no delay and sum", Mixings())
	}
}
