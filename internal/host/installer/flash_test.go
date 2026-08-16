package installer

import (
	"reflect"
	"strings"
	"testing"
)

// Both answers are consulted because they can disagree, and the disagreement is the case that matters:
// an image asking for permissive on a kernel that is enforcing leaves echod unable to open its socket,
// which reads as a bug in echod rather than a device that needs reflashing.
func TestPermissiveNeedsBothAnswers(t *testing.T) {
	for _, tc := range []struct {
		asked, enforcing string
		want             bool
	}{
		{"permissive", "Permissive", true},
		{"permissive", "", true},
		{"permissive", "Enforcing", false},
		{"permissive", "enforcing", false},
		{"enforce", "Permissive", false},
		{"enforce", "Enforcing", false},
		{"", "", false},
	} {
		if got := permissiveFrom(tc.asked, tc.enforcing); got != tc.want {
			t.Errorf("permissiveFrom(%q, %q) = %t, want %t", tc.asked, tc.enforcing, got, tc.want)
		}
	}
}

// Everything that writes is skipped on a device that is ready, which is what makes the stage safe to
// re-run. Root alone is not ready, and permissive alone is not either.
func TestReadyNeedsRootAndPermissive(t *testing.T) {
	for _, tc := range []struct {
		rooted, permissive, want bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	} {
		s := state{rooted: tc.rooted, permissive: tc.permissive}
		if got := s.ready(); got != tc.want {
			t.Errorf("ready(root=%t permissive=%t) = %t, want %t",
				tc.rooted, tc.permissive, got, tc.want)
		}
	}
}

func TestStateSaysWhatItFound(t *testing.T) {
	s := state{rooted: false, permissive: false, enforcing: "Enforcing"}

	said := s.String()
	for _, want := range []string{"root=false", "enforcing"} {
		if !strings.Contains(said, want) {
			t.Errorf("%q does not mention %q", said, want)
		}
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

	r.state = state{rooted: false, permissive: false}
	if _, skip := r.done(); skip {
		t.Error("a device with neither root nor permissive is skipping the write")
	}
}

// checkApproval is the last gate: nothing is written without someone having said so.
func TestNothingIsWrittenWithoutApproval(t *testing.T) {
	r := &run{cfg: Config{}, state: state{rooted: false, permissive: false}}

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

func TestParseUserdataMounts(t *testing.T) {
	raw := strings.Join([]string{
		"rootfs / rootfs rw 0 0",
		"/dev/block/mmcblk0p16 /data ext4 rw 0 0",
		"/dev/block/mmcblk0p16 /sdcard ext4 rw 0 0",
		"/dev/block/mmcblk0p1 /system ext4 ro 0 0",
	}, "\n")

	got, err := parseUserdataMounts(raw, "/dev/block/mmcblk0p16")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/sdcard", "/data"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseUserdataMountsAcceptsByNameSource(t *testing.T) {
	raw := userdataNode + " /data ext4 rw 0 0\n"

	got, err := parseUserdataMounts(raw, "/dev/block/mmcblk0p16")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/data"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseUserdataMountsRefusesUnexpectedTarget(t *testing.T) {
	raw := "/dev/block/mmcblk0p16 /unexpected ext4 rw 0 0\n"

	if _, err := parseUserdataMounts(raw, "/dev/block/mmcblk0p16"); err == nil {
		t.Fatal("accepted an unexpected userdata mount")
	}
}
