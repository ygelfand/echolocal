//go:build board_athena

package mic

import (
	"slices"
	"testing"

	"github.com/ygelfand/echolocal/internal/config"
)

// beam.go steers a 7-mic ring; athena's 4 mics are not one.
func TestAthenaDoesNotOfferDelayAndSum(t *testing.T) {
	if slices.Contains(Mixings(), config.MixDelaySum) {
		t.Errorf("athena offers %v, want no delay and sum", Mixings())
	}
}
