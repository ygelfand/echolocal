// Package update owns replacing echod with a newer echod, and getting back to a working one when
// that goes wrong.
//
// The shape of it is that nothing trusts a new binary until it has run for a while. An update leaves
// the one it replaced beside it as echod.prev, and only a process that has been alive long enough to
// be believed deletes it. So echod.prev existing means an update is still on trial, and that single
// fact drives everything here and the boot hook that backs it up.
package update

import (
	"log/slog"
	"os"

	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/prop"
)

// Paths and properties are variables rather than constants so a test can point them somewhere it is
// allowed to write.
var (
	prev = layout.PrevBinary
	old  = layout.OldBinary

	mount = "/system"

	// writable is the remount, kept as a variable so a test can run everything around it somewhere it
	// is already allowed to write.
	writable = remount
)

// wanted carries a request to restart into whatever is supervising the process, which is the only
// thing that can unwind the hardware cleanly. Buffered and dropped when full: two requests are one
// restart.
var wanted = make(chan string, 1)

// Restart asks for the process to be replaced by a new one of itself. It does not exit: the caller has
// no idea what state the speaker or the ring are in, and finishing with the amplifier still driven is
// what makes the device pop.
func Restart(why string) {
	select {
	case wanted <- why:
	default:
	}
}

// Wanted is how the supervisor hears about it.
func Wanted() <-chan string { return wanted }

// OnTrial reports whether an update is waiting to be believed.
func OnTrial() bool {
	_, err := os.Stat(prev)
	return err == nil
}

// Start is the first thing echod does about an update it may have installed, and it either carries on
// or reboots.
//
// A trial is opened by the process that installed the update; if this process finds one already open,
// the last one took the binary and died without committing, and trying again would join init's restart
// loop forever. Rebooting hands the decision to the boot hook, which is outside the binary and can put
// the old one back.
//
// It reports whether echod is on trial, so a caller can say so and commit later, and whether a reboot
// has been asked for — in which case there is no point taking the hardware, and the caller should get
// out of the way of it.
func Start() (onTrial, rebooting bool) {
	if !OnTrial() {
		return false, false
	}

	if was, _ := prop.Get(layout.TrialProp); was != "" {
		slog.Error("this binary was already tried this boot and never settled, rebooting to go back",
			"trial", was, "prev", prev)
		reboot()
		return true, true
	}

	if err := prop.Set(layout.TrialProp, "1"); err != nil {
		slog.Error("marking the update as on trial failed", "err", err)
	}
	slog.Warn("running an update on trial", "prev", prev)
	return true, false
}

// Commit keeps an update: the binary it replaced becomes one generation back rather than the thing a
// boot would restore. Called once echod has been running long enough to be worth believing, which is
// the only evidence available — there is nothing else to ask.
//
// Doing nothing is the normal case, since most starts have no update behind them.
func Commit() {
	if !OnTrial() {
		return
	}

	if err := writable(true); err != nil {
		slog.Error("remounting to keep an update failed", "err", err)
		return
	}
	defer func() {
		if err := writable(false); err != nil {
			slog.Error("remounting read-only failed", "err", err)
		}
	}()

	if err := os.Rename(prev, old); err != nil {
		slog.Error("keeping an update failed", "from", prev, "to", old, "err", err)
		return
	}
	if err := prop.Set(layout.TrialProp, ""); err != nil {
		slog.Error("clearing the trial property failed", "err", err)
	}
	slog.Info("update kept", "previous", old)
}

// RolledBack is what the boot hook left behind if it had to put the previous binary back, and clearing
// it is this process saying it has been noticed. Empty means the last boot was ordinary.
func RolledBack() string {
	was, err := prop.Get(layout.RolledBackProp)
	if err != nil || was == "" {
		return ""
	}

	slog.Warn("an update was rolled back before this boot", "version", was)
	if err := prop.Set(layout.RolledBackProp, ""); err != nil {
		slog.Error("clearing the rollback property failed", "err", err)
	}
	return was
}

// reboot asks init for a clean one, which unwinds the services it started rather than dropping the
// device where it stands.
func reboot() {
	if err := prop.Set("sys.powerctl", "reboot"); err != nil {
		slog.Error("asking init to reboot failed", "err", err)
	}
}
