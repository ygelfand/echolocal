package echoctl

import "testing"

func TestRebootChoiceOf(t *testing.T) {
	for _, tc := range []struct {
		yes, no bool
		want    rebootChoice
	}{
		{want: rebootAsk},
		{yes: true, want: rebootYes},
		{no: true, want: rebootNo},
	} {
		if got := rebootChoiceOf(tc.yes, tc.no); got != tc.want {
			t.Errorf("rebootChoiceOf(%t, %t) = %d, want %d", tc.yes, tc.no, got, tc.want)
		}
	}
}

