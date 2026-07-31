package update

import (
	"os"
	"path/filepath"
	"testing"
)

// somewhere writable points the paths at a temp directory, since the real ones are under a read-only
// /system that only exists on the device.
func somewhere(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	t.Cleanup(func() { prev, old, mount, writable = layoutPrev, layoutOld, layoutMount, remount })
	prev, old, mount = filepath.Join(dir, "echod.prev"), filepath.Join(dir, "echod.old"), dir

	// The directory is already writable, so the remount is the one thing that cannot be exercised here.
	writable = func(bool) error { return nil }
	return dir
}

var (
	layoutPrev  = prev
	layoutOld   = old
	layoutMount = mount
)

func TestOnTrialIsThePresenceOfPrev(t *testing.T) {
	somewhere(t)

	if OnTrial() {
		t.Error("on trial with no previous binary")
	}
	if err := os.WriteFile(prev, []byte("older"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !OnTrial() {
		t.Error("not on trial with a previous binary sitting there")
	}
}

// Committing files the previous binary one generation back, which is what stops a later boot from
// treating it as a trial that never finished. Remounting fails off the device, so what is asserted here
// is the rename, on a directory that is already writable.
func TestCommitFilesThePreviousBinaryAway(t *testing.T) {
	somewhere(t)

	if err := os.WriteFile(prev, []byte("older"), 0o755); err != nil {
		t.Fatal(err)
	}
	Commit()

	if OnTrial() {
		t.Error("still on trial after committing")
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("the previous binary was not kept: %v", err)
	}
}

// Nothing to keep is the ordinary case — most starts follow no update at all — and it must not touch
// the filesystem or report anything.
func TestCommitWithNothingOnTrial(t *testing.T) {
	dir := somewhere(t)

	Commit()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("committing nothing left %d files behind", len(entries))
	}
}

func TestStartWithNoUpdateSaysSo(t *testing.T) {
	somewhere(t)

	onTrial, rebooting := Start()
	if onTrial || rebooting {
		t.Errorf("no previous binary reported trial=%t rebooting=%t", onTrial, rebooting)
	}
}

// Two requests are one restart: the channel is buffered at one and a full one is dropped, so a caller
// that asks twice cannot leave a second request behind to be acted on later.
func TestRestartCoalesces(t *testing.T) {
	Restart("first")
	Restart("second")

	if got := <-Wanted(); got != "first" {
		t.Errorf("wanted %q, want first", got)
	}
	select {
	case got := <-Wanted():
		t.Errorf("a second request was queued: %q", got)
	default:
	}
}
