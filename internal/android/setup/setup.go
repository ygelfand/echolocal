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
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/ygelfand/echolocal/internal/android/dns"
	"github.com/ygelfand/echolocal/internal/android/firewall"
	"github.com/ygelfand/echolocal/internal/android/prop"
	"github.com/ygelfand/echolocal/internal/component"
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

// stop asks init to stop a vendor service.
func stop(service, reason string) Action {
	return Action{
		Name:   "stop " + service,
		Reason: reason,
		Do:     func() error { return prop.Stop(service) },
	}
}

// Actions is applied in order. Nothing here is allowed to fail a boot.
var Actions = []Action{
	stop("meshmgrservice", "the other half of the BLE mesh stack"),
	stop("whad_cc", "whole-home audio's control channel, left with nothing once whad is hidden"),
	stop("vitals_service", "collects the device's vitals for Amazon"),
	stop("perfmonitord", "Amazon's performance monitoring"),
	stop("perfrecoveryd", "Amazon's performance monitoring"),
	stop("avahi-daemon", "Amazon's mDNS, for Spotify Connect; echod advertises itself"),
	stop("drm", "drmserver, for protected media playback"),
	{
		Name: "take the pin controller back from mediaserver",
		Reason: "the vendor audio HAL clears the microphone mute line while it sets up an input path, " +
			"which unmutes a device the user left muted",
		// Amazon's csm_audio_init.sh grants it: "mediaserver service needs to access mt_gpio file in
		// order to access mute LED on MUTE button ... chown root.media". Dropping the group write takes
		// it back. echod drives the line through /sys/class/gpio, which is root's, so this costs us
		// nothing and leaves mediaserver running — stopping it is not an option, because AudioService
		// inside system_server then retries forever and says so every time.
		//
		// Every boot: sysfs modes are the kernel's defaults again after a restart, and the vendor
		// script re-grants it.
		Do: func() error { return os.Chmod(pinControl, 0o644) },
	},
	{
		Name:   "silence AmazonUsageStatsService",
		Reason: "logs the network state on a timer from inside system_server",
		// The persist form of this property is 39 bytes, past what Android 5.1 accepts, so it
		// cannot be set once at install and has to be set again on every boot.
		Do: func() error { return prop.Set("log.tag.AmazonUsageStatsService", "S") },
	},
	{
		Name:   "size the runtime to the cores that are present",
		Reason: "the kernel hotplugs them, so Go sees however many were online when it started",
		// GOMAXPROCS is read once at start-up. Pinning cores against the governor is the answer we
		// rejected; counting them and telling the runtime is the one that works.
		Do: procs,
	},
	{
		Name:   "point the resolver at the nameservers the platform knows",
		Reason: "there is no /etc/resolv.conf, so Go's resolver has nowhere to look",
		Do:     func() error { dns.Use(); return nil },
	},
}

// Late is applied once Android reports the boot finished, for the services init starts from that
// and no earlier. A stop sent before then is undone by the start that follows it.
var Late = []Action{
	stop("shblemeshd", "BLE mesh daemon left with nothing to talk to once its service package is hidden"),
	{
		Name: "put the firewall jumps back",
		Reason: "the boot completing is what starts firewall.sh, which flushes INPUT: our chains keep " +
			"their rules but nothing reaches them again until INPUT jumps to them",
		Do: firewall.Ensure,
	},
}

// How long ApplyLate waits for the boot to finish, and how often it looks. Nothing is waiting on
// these stops, so the interval is loose.
const (
	bootProp = "sys.boot_completed"
	bootWait = 3 * time.Minute
	bootPoll = 5 * time.Second
)

// Apply runs every action, reporting what failed. A failure is logged and passed over: none of this
// is worth refusing to start over.
func Apply() { apply("boot setup", Actions) }

// Setup is the platform prep as a component, so it takes its turn in the same ordered list as
// everything else. First: what follows expects the resolver to work and the runtime to be the right
// size, and the vendor services it stops are the ones that fight over the hardware.
type Setup struct{}

func init() { component.Register(component.Hardware, Setup{}, component.Order(1)) }

func (Setup) Name() string { return "platform setup" }

func (Setup) Start(context.Context) error {
	Apply()
	return nil
}

// Run finishes the job once Android says the boot completed, then returns. The stops that wait for
// that are undone by the start that would otherwise follow them.
func (Setup) Run(ctx context.Context) error {
	ApplyLate(ctx)
	return nil
}

// ApplyLate waits for the boot to finish and then applies Late. It is meant to be run in its own
// goroutine, and returns without applying anything if the boot never completes.
func ApplyLate(ctx context.Context) {
	if !bootCompleted(ctx) {
		return
	}
	apply("late setup", Late)
}

func apply(what string, actions []Action) {
	var failed int
	for _, a := range actions {
		if err := a.Do(); err != nil {
			slog.Error(what+" failed", "action", a.Name, "reason", a.Reason, "err", err)
			failed++
		}
	}
	slog.Info(what+" applied", "actions", len(actions), "failed", failed)
}

// bootCompleted reports whether the boot finished before bootWait ran out.
func bootCompleted(ctx context.Context) bool {
	tick := time.NewTicker(bootPoll)
	defer tick.Stop()
	giveUp := time.After(bootWait)

	for {
		if v, err := prop.Get(bootProp); err == nil && v == "1" {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-giveUp:
			slog.Warn("gave up waiting for the boot to finish", "after", bootWait, "skipped", len(Late))
			return false
		case <-tick.C:
		}
	}
}
