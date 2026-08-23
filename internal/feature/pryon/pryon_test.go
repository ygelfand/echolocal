package pryon

import (
	"testing"

	"github.com/ygelfand/echolocal/internal/lib/wake"
)

func TestSelectedSlot(t *testing.T) {
	for name, tc := range map[string]struct {
		active []string
		want   int
	}{
		"first":     {[]string{wake.PryonID, "okay_nabu"}, 0},
		"second":    {[]string{"okay_nabu", wake.PryonID}, 1},
		"not armed": {[]string{"okay_nabu"}, -1},
	} {
		if got := selectedSlot(tc.active); got != tc.want {
			t.Errorf("%s: got slot %d, want %d", name, got, tc.want)
		}
	}
}
