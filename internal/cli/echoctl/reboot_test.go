package echoctl

import (
	"context"
	"io"
	"testing"

	"github.com/ygelfand/echolocal/internal/installer"
)

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

// Whether a reboot is worth offering comes from the steps themselves, so what render reports has to
// follow the statuses and nothing else. Tests run without a terminal, which is render's plain path.
func TestRenderReportsWhetherAnythingChanged(t *testing.T) {
	for name, tc := range map[string]struct {
		statuses []installer.Status
		want     bool
	}{
		"all skipped":  {[]installer.Status{installer.Skipped, installer.Skipped}, false},
		"one did work": {[]installer.Status{installer.Skipped, installer.Done}, true},
		"no steps":     {nil, false},
	} {
		changed, err := render(context.Background(), io.Discard, "t", "ok",
			func(report installer.Reporter) error {
				for i, st := range tc.statuses {
					report(installer.Event{Step: i + 1, Total: len(tc.statuses), Name: "step", Status: installer.Running})
					report(installer.Event{Step: i + 1, Total: len(tc.statuses), Name: "step", Status: st})
				}
				return nil
			})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if changed != tc.want {
			t.Errorf("%s: changed = %t, want %t", name, changed, tc.want)
		}
	}
}
