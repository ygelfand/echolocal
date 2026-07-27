// Package installer installs echod as the ledcontroller service.
package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ygelfand/echolocal/internal/device"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/services"
)

type Status int

const (
	Running Status = iota
	Done
	Skipped
	Failed
)

// Event reports one step's progress: Running first, then exactly one terminal status.
type Event struct {
	Step   int
	Total  int
	Name   string
	Status Status
	Detail string
	Err    error
}

// Reporter receives events as the install runs.
type Reporter func(Event)

// Config is what an install needs from the caller.
type Config struct {
	// EchodPath is the host path to the arm binary to install.
	EchodPath string

	// Name is what Home Assistant calls the device. Only needed on a device that has none.
	Name string

	// ZeroPSK leaves the device without a key, running Noise with the reserved zero key so
	// Home Assistant can push one. Nothing to paste, but until HA does that anyone on the
	// network can drive the device.
	ZeroPSK bool
}

type step struct {
	name string
	run  func(*run) (detail string, skipped bool, err error)
}

var steps = []step{
	{"check device", checkDevice},
	{"hide Amazon packages", hidePackages},
	{"device name", installName},
	{"encryption key", installKey},
	{"remount /system rw", remountRW},
	{"install echod", installBinary},
	{"back up stock ledcontroller", backupService},
	{"take over ledcontroller service", takeOverService},
	{"open the API port", installFirewallHook},
	{"stop service", stopService},
	{"remount /system ro", remountRO},
	{"start echod", startService},
}

var restartSteps = []step{
	{"stop echod", stopService},
	{"start echod", startService},
}

var uninstallSteps = []step{
	{"stop echod", stopService},
	{"remount /system rw", remountRW},
	{"close the API port", removeFirewallHook},
	{"restore stock ledcontroller", restoreService},
	{"remove echod", removeBinary},
	{"remount /system ro", remountRO},
	{"start ledcontroller", startStock},
}

type run struct {
	d   *device.Device
	cfg Config
}

// Install is safe to re-run: every step either checks the state it creates or is harmless to
// repeat. The stock binary is backed up only once, so a second run cannot overwrite it.
func Install(ctx context.Context, d *device.Device, cfg Config, report Reporter) error {
	return execute(ctx, steps, &run{d: d, cfg: cfg}, report)
}

// Uninstall puts Amazon's ledcontroller back and removes echod.
func Uninstall(ctx context.Context, d *device.Device, report Reporter) error {
	return execute(ctx, uninstallSteps, &run{d: d}, report)
}

// Restart cycles echod through init.
func Restart(ctx context.Context, d *device.Device, report Reporter) error {
	return execute(ctx, restartSteps, &run{d: d}, report)
}

func execute(ctx context.Context, steps []step, r *run, report Reporter) error {
	// A failure part way through must not leave /system writable.
	defer func() { _, _ = r.d.Shell("mount -o remount,ro /system") }()

	for i, s := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		ev := Event{Step: i + 1, Total: len(steps), Name: s.name}

		report(ev)
		detail, skipped, err := s.run(r)
		switch {
		case err != nil:
			ev.Status, ev.Err = Failed, err
			report(ev)
			return fmt.Errorf("%s: %w", s.name, err)
		case skipped:
			ev.Status, ev.Detail = Skipped, detail
		default:
			ev.Status, ev.Detail = Done, detail
		}
		report(ev)
	}
	return nil
}

func checkDevice(r *run) (string, bool, error) {
	if r.cfg.EchodPath == "" {
		return "", false, errors.New("no echod binary given")
	}
	if _, err := os.Stat(r.cfg.EchodPath); err != nil {
		return "", false, err
	}

	sdk, err := r.d.Getprop("ro.build.version.sdk")
	if err != nil {
		return "", false, err
	}
	if sdk != "22" {
		return "", false, fmt.Errorf("device reports SDK %s, want 22", sdk)
	}
	model, _ := r.d.Getprop("ro.product.device")
	return fmt.Sprintf("%s, SDK %s, %s", model, sdk, r.d.Serial()), false, nil
}

func remountRW(r *run) (string, bool, error) {
	_, err := r.d.Shell("mount -o remount,rw /system")
	return "", false, err
}

func installBinary(r *run) (string, bool, error) {
	if _, err := r.d.Shell("mkdir -p " + layout.Dir); err != nil {
		return "", false, err
	}
	if err := r.d.PushFile(r.cfg.EchodPath, layout.Binary, 0o755); err != nil {
		return "", false, err
	}
	label, err := r.d.Label(layout.Binary)
	if err != nil {
		return "", false, err
	}
	return label, false, nil
}

// backupService keeps Amazon's binary. Moving it a second time would move our own symlink
// onto the backup and lose the original for good, so this only ever runs once.
func backupService(r *run) (string, bool, error) {
	have, err := r.d.Exists(layout.Backup)
	if err != nil {
		return "", false, err
	}
	if have {
		return "already saved at " + layout.Backup, true, nil
	}
	if _, err := r.d.Shell(fmt.Sprintf("mv %s %s", layout.Service, layout.Backup)); err != nil {
		return "", false, err
	}
	return layout.Backup, false, nil
}

func takeOverService(r *run) (string, bool, error) {
	_, err := r.d.Shell(fmt.Sprintf("rm -f %s && ln -s %s %s", layout.Service, layout.Binary, layout.Service))
	if err != nil {
		return "", false, err
	}
	target, err := r.d.Shell("readlink " + layout.Service)
	return strings.TrimSpace(target), false, err
}

// stopService releases the running binary: /system cannot be remounted read-only while a
// process is mapped to a file that was just overwritten.
//
// init publishes a transient "stopping" before the process is reaped, so only "stopped" means
// the mapping is gone.
func stopService(r *run) (string, bool, error) {
	if err := r.d.Setprop("ctl.stop", layout.ServiceName); err != nil {
		return "", false, err
	}

	var state string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		if state, err = r.d.Getprop("init.svc." + layout.ServiceName); err != nil {
			return "", false, err
		}
		if state == "stopped" {
			return state, false, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", false, fmt.Errorf("service is %q 10s after ctl.stop, want stopped", state)
}

func remountRO(r *run) (string, bool, error) {
	_, err := r.d.Shell("mount -o remount,ro /system")
	return "", false, err
}

// hidePackages releases the hardware echod needs: Amazon's audio clients keep mediaserver
// holding the PCM devices, and one of them owns the mute button. It runs before anything
// touches /system, so a failure here leaves the filesystem untouched.
func hidePackages(r *run) (string, bool, error) {
	hid, stopped, err := services.Hide(r.d)
	if err != nil {
		return "", false, err
	}
	if len(hid) == 0 && len(stopped) == 0 {
		return fmt.Sprintf("%d already hidden, none running", len(services.Hidden)), true, nil
	}
	return fmt.Sprintf("hid %d of %d, stopped %d", len(hid), len(services.Hidden), len(stopped)), false, nil
}

// startService hands control back to init and waits for echod to report itself, since
// ctl.start returns before the process has run. echod publishes its start as an uptime, which
// only moves forward within a boot, so a changed value means this run rather than the last.
func startService(r *run) (string, bool, error) {
	before, _ := r.d.Getprop(layout.StartedProp)

	if err := r.d.Setprop("ctl.start", layout.ServiceName); err != nil {
		return "", false, err
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		started, err := r.d.Getprop(layout.StartedProp)
		if err != nil {
			return "", false, err
		}
		if started != "" && started != before {
			return "started at uptime " + started, false, nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	state, _ := r.d.Getprop("init.svc." + layout.ServiceName)
	return "", false, fmt.Errorf("echod did not report a start within 10s (init.svc.%s=%s)", layout.ServiceName, state)
}

// restoreService refuses to run without the backup: removing our symlink without putting the
// stock binary back would leave init with a service definition pointing at nothing.
func restoreService(r *run) (string, bool, error) {
	have, err := r.d.Exists(layout.Backup)
	if err != nil {
		return "", false, err
	}
	if !have {
		link, err := r.d.IsSymlink(layout.Service)
		if err != nil {
			return "", false, err
		}
		if !link {
			return "not installed", true, nil
		}
		return "", false, fmt.Errorf("%s is missing; restore it before uninstalling", layout.Backup)
	}

	if _, err := r.d.Shell(fmt.Sprintf("rm -f %s && mv %s %s", layout.Service, layout.Backup, layout.Service)); err != nil {
		return "", false, err
	}

	// mv preserves the label, so this confirms rather than fixes.
	label, err := r.d.Label(layout.Service)
	if err != nil {
		return "", false, err
	}
	if label != layout.StockLabel {
		if err := r.d.Chcon(layout.StockLabel, layout.Service); err != nil {
			return "", false, err
		}
		return "relabelled to " + layout.StockLabel, false, nil
	}
	return label, false, nil
}

func removeBinary(r *run) (string, bool, error) {
	have, err := r.d.Exists(layout.Dir)
	if err != nil {
		return "", false, err
	}
	if !have {
		return "nothing to remove", true, nil
	}
	_, err = r.d.Shell("rm -rf " + layout.Dir)
	return layout.Dir, false, err
}

func startStock(r *run) (string, bool, error) {
	if err := r.d.Setprop("ctl.start", layout.ServiceName); err != nil {
		return "", false, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, err := r.d.Getprop("init.svc." + layout.ServiceName)
		if err != nil {
			return "", false, err
		}
		if state == "running" {
			return state, false, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", false, errors.New("stock ledcontroller did not start")
}
