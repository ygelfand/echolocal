//go:build !board_checkers && !board_cronos && !board_crown && !board_athena

package mic

import (
	"slices"
	"testing"

	"github.com/ygelfand/echolocal/internal/config"
)

func TestTheDotOffersDelayAndSum(t *testing.T) {
	if !slices.Contains(Mixings(), config.MixDelaySum) {
		t.Errorf("the dot offers %v, want delay and sum among them", Mixings())
	}
}
