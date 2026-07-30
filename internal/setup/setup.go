// Package setup applies the device changes echod wants in place on every boot.
//
// Only idempotent, atomic actions belong here. Idempotent because this runs on every start,
// including a restart, and must not care whether the last one got there first; atomic because a
// step that failed half way through would leave the device in a state nothing else expects.
//
// A property write and a stop request both qualify: init either accepts the message or does not.
// Anything that has to read state, edit a file, or take more than one step to be true does not
// belong here — that is provisioning's job, where it can be checked and rolled back.
package setup

import (
	"log/slog"
	"os"

	"github.com/ygelfand/echolocal/internal/prop"
)

// Action is one change and why echod wants it.
type Action struct {
	Name   string
	Reason string
	Do     func() error
}

// Actions is applied in order. Nothing here is allowed to fail a boot.
var Actions = []Action{
	{
		Name:   "stop shblemeshd",
		Reason: "BLE mesh daemon left with nothing to talk to once its service package is hidden",
		Do:     func() error { return prop.Stop("shblemeshd") },
	},
	{
		Name:   "silence AmazonUsageStatsService",
		Reason: "logs the network state on a timer from inside system_server",
		// The persist form of this property is 39 bytes, past what Android 5.1 accepts, so it
		// cannot be set once at install and has to be set again on every boot.
		Do: func() error { return prop.Set("log.tag.AmazonUsageStatsService", "S") },
	},
}

// Apply runs every action, reporting what failed. A failure is logged and passed over: none of this
// is worth refusing to start over.
func Apply() {
	var failed int
	for _, a := range Actions {
		if err := a.Do(); err != nil {
			slog.Error("boot setup failed", "action", a.Name, "reason", a.Reason, "err", err)
			failed++
		}
	}
	slog.Info("boot setup applied", "actions", len(Actions), "failed", failed)
}
