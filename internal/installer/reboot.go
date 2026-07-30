package installer

import (
	"context"
	"fmt"
	"time"

	"github.com/ygelfand/echolocal/internal/device"
	"github.com/ygelfand/echolocal/internal/layout"
)

// Several install steps only land on the next boot: the gating properties init reads at start, the
// firewall hook Amazon's own script runs, taking over a service that was already running. echod is
// started at the end so the device works immediately, but that is not the same device as one that
// booted with all of it in place, and the first boot is where a mistake shows up.
//
// So this stage exists to have that boot happen while somebody is watching, and to say whether echod
// came back by itself — the only part of an install that is not the installer describing its own work.

// residentTimeout is how long echod gets to publish itself after Android is up. It starts as an init
// service, so it is running well before this.
const residentTimeout = 60 * time.Second

var rebootSteps = []step{
	{"reboot the device", rebootDevice},
	{"wait for Android", awaitAndroid},
	{"echod started on its own", awaitResident},
}

// RebootAndWait restarts the device and waits for echod to come back without being told to. Not
// Restart, which cycles echod through init while Android keeps running.
func RebootAndWait(ctx context.Context, d *device.Device, report Reporter) error {
	return execute(ctx, rebootSteps, &run{d: d, ctx: ctx}, report)
}

func rebootDevice(r *run) (string, bool, error) {
	return "", false, r.d.Reboot("")
}

func awaitAndroid(r *run) (string, bool, error) {
	ctx, cancel := context.WithTimeout(r.ctx, androidTimeout)
	defer cancel()

	if err := r.d.WaitBooted(ctx); err != nil {
		return "", false, err
	}

	up, err := r.d.Uptime()
	if err != nil {
		return "booted", false, nil
	}
	return fmt.Sprintf("booted, %.0fs up", up), false, nil
}

// awaitResident waits for echod's own word for it. The properties it publishes do not survive a
// reboot, so anything read here was published by this boot.
func awaitResident(r *run) (string, bool, error) {
	deadline := time.Now().Add(residentTimeout)

	for {
		state, err := r.d.Getprop(layout.StateProp)
		if err != nil {
			return "", false, err
		}
		if state == "resident" {
			started, _ := r.d.Getprop(layout.StartedProp)
			return "resident, started at uptime " + started, false, nil
		}
		if time.Now().After(deadline) {
			svc, _ := r.d.Getprop("init.svc." + layout.ServiceName)
			return "", false, fmt.Errorf("echod is %q %s after boot (init.svc.%s=%q)",
				state, residentTimeout, layout.ServiceName, svc)
		}

		select {
		case <-r.ctx.Done():
			return "", false, r.ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
