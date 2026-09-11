package installer

import (
	"strings"
	"testing"
)

// Everything that writes is skipped on a device that is ready, which is what makes the stage safe to
// re-run. Recovery is never ready: its root says nothing about the installed system.
func TestReadyNeedsRootAndPermissiveOutsideRecovery(t *testing.T) {
	for _, tc := range []struct {
		rooted, permissive, recovery, want bool
	}{
		{true, true, false, true},
		{true, true, true, false},
		{true, false, false, false},
		{false, true, false, false},
		{false, false, false, false},
		{false, false, true, false},
	} {
		s := state{rooted: tc.rooted, permissive: tc.permissive, recovery: tc.recovery}
		if got := s.ready(); got != tc.want {
			t.Errorf("ready(root=%t permissive=%t recovery=%t) = %t, want %t",
				tc.rooted, tc.permissive, tc.recovery, got, tc.want)
		}
	}
}

func TestStateSaysWhatItFound(t *testing.T) {
	if said := (state{rooted: false}).String(); !strings.Contains(said, "root=false") {
		t.Errorf("%q does not say whether it found root", said)
	}
}

// A ready device skips the writing steps, and the detail says why rather than leaving a bare bullet in
// the progress display.
func TestWritingStepsSkipWhenReady(t *testing.T) {
	r := &run{state: state{rooted: true, permissive: true}}

	detail, skip := r.done()
	if !skip {
		t.Fatal("a ready device is not skipping the writing steps")
	}
	if detail == "" {
		t.Error("skipped with no reason given")
	}

	r.state = state{rooted: false}
	if _, skip := r.done(); skip {
		t.Error("a device without root is skipping the write")
	}
}

// checkApproval is the last gate: nothing is written without someone having said so.
func TestNothingIsWrittenWithoutApproval(t *testing.T) {
	r := &run{cfg: Config{}, state: state{rooted: false}}

	if _, _, err := checkApproval(r); err == nil {
		t.Fatal("unapproved write was allowed")
	}

	r.cfg.Approved = true
	if _, _, err := checkApproval(r); err != nil {
		t.Errorf("approved write refused: %v", err)
	}

	// A device needing nothing is not asked about at all.
	r.cfg.Approved = false
	r.state = state{rooted: true, permissive: true}
	_, skip, err := checkApproval(r)
	if err != nil || !skip {
		t.Errorf("ready device: skip=%t err=%v, want skipped with no error", skip, err)
	}
}

// A stage with nothing to write refuses rather than writing whatever it was handed.
func TestCheckImageRefusesAnEmptyOne(t *testing.T) {
	r := &run{state: state{device: "biscuit", build: "272.6.8.0_user_680767620"}}

	if _, _, err := checkImage(r); err == nil {
		t.Error("accepted an empty image")
	}
}
