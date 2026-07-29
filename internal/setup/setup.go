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

// pinControl dumps and drives every pin on the SoC. It is how the vendor audio HAL reaches the
// microphone mute line, which is the one thing here that must be nobody else's.
const pinControl = "/sys/devices/soc/1000b000.pinctrl/mt_gpio"

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
		Name: "take the pin controller back from mediaserver",
		Reason: "the vendor audio HAL clears the microphone mute line while it sets up an input path, " +
			"which unmutes a device the user left muted",
		// Amazon's own csm_audio_init.sh grants this: "mediaserver service needs to access mt_gpio
		// file in order to access mute LED on MUTE button ... chown root.media". Dropping the group
		// write takes it back. echod writes the line through /sys/class/gpio, which is root's, so this
		// costs us nothing and leaves mediaserver running — stopping it is not an option, because
		// AudioService inside system_server then retries forever and says so in the log every time.
		//
		// Every boot rather than once at install: sysfs modes are the kernel's defaults again after a
		// restart, and the vendor script re-grants it.
		Do: func() error { return os.Chmod(pinControl, 0o644) },
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
