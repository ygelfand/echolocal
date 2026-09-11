// Package installer installs echod as the ledcontroller service.
package installer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ygelfand/echolocal/internal/host/device"
	"github.com/ygelfand/echolocal/internal/layout"
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
	// Echod is the arm binary to install: contents, so an embedded payload and a file arrive the
	// same way.
	Echod []byte

	// Name is what Home Assistant calls the device. Only needed on a device that has none.
	Name string

	// ZeroPSK leaves the device without a key, running Noise with the reserved zero key so
	// Home Assistant can push one. Nothing to paste, but until HA does that anyone on the
	// network can drive the device.
	ZeroPSK bool

	// BootImage is the image the flash stage writes, and BootImageFrom where it came from.
	BootImage     []byte
	BootImageFrom string

	// Approved records that someone agreed to the boot partition being overwritten. Nothing is
	// written without it: the caller asks, because by the time a stage runs the terminal belongs to
	// the progress display.
	Approved bool
}

type step struct {
	name string
	run  func(*run) (detail string, skipped bool, err error)
}

var steps = []step{
	{"check device", checkDevice},
	{"device name", installName},
	{"encryption key", installKey},
	{"default wake words", installModels},
	{"selinux permissive", checkPermissive},
	{"remount /system rw", remountRW},
	{"stop and disable Amazon services", deAmazon},
	{"clear the saved usb config", clearUSBConfig},
	{"install echod", installBinary},
	{"back up stock ledcontroller", backupService},
	{"take over ledcontroller service", takeOverService},
	{"disable the boot animation", disableBootAnimation},
	{"open the API port", installFirewallHook},
	{"stop service", stopService},
	{"remount /system ro", remountRO},
	{"start echod", startService},
}

var restartSteps = []step{
	{"stop echod", stopService},
	{"start echod", startService},
}

type run struct {
	d   *device.Device
	cfg Config

	// ctx is the caller's, for the steps that wait on a device rebooting.
	ctx context.Context

	// state is what the device last said about itself. The flash stage reads it before deciding to
	// write anything and again afterwards to judge whether it worked.
	state state

	// reboot is or-ed by the steps that change something init only acts on at start-up. The rest of a
	// run — checks, remounts, writing the binary, restarting the service — happens every time and
	// proves nothing about the next boot, so this is what decides whether a reboot is worth offering.
	reboot bool
}

// Install puts echod on a device that already has root. It is safe to re-run: every step either
// checks the state it creates or is harmless to repeat, and the stock binary is backed up only once,
// so a second run cannot overwrite it.
//
// It reports whether anything changed that the device will only act on when it next starts, which is
// the only reason to offer a reboot. A re-run that just replaced the binary reports false: echod is
// started again here, so there is nothing a restart would settle.
//
// FlashBoot has to have happened first, on this device or a previous run. Nothing here works without
// a root adbd, and reading the device's own name needs it.
func Install(ctx context.Context, d *device.Device, cfg Config, report Reporter) (bool, error) {
	r := &run{d: d, cfg: cfg, ctx: ctx}
	err := execute(ctx, steps, r, report)
	return r.reboot, err
}

// FlashBoot writes echod's boot image, and does nothing on a device that already has root and a
// permissive kernel. Its own stage because it reboots twice, is the only thing that writes outside
// /system, and is worth running on its own as often as needed.
func FlashBoot(ctx context.Context, d *device.Device, cfg Config, report Reporter) error {
	return execute(ctx, flashSteps, &run{d: d, cfg: cfg, ctx: ctx}, report)
}

// Restart cycles echod through init.
func Restart(ctx context.Context, d *device.Device, report Reporter) error {
	return execute(ctx, restartSteps, &run{d: d}, report)
}

func execute(ctx context.Context, steps []step, r *run, report Reporter) error {
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
	if len(r.cfg.Echod) == 0 {
		return "", false, errors.New("no echod binary given")
	}

	sdk, err := r.d.Getprop("ro.build.version.sdk")
	if err != nil {
		return "", false, err
	}
	// Fire OS 6 is Android 7.1, SDK 25. Fire OS 5 devices are SDK 22 and are not installed to any more:
	// the image, the partition layout and the binary all differ, and an older release still does it.
	if sdk != "25" {
		return "", false, fmt.Errorf("device reports SDK %s, want 25 (Fire OS 6); Fire OS 5 needs echoctl 0.0.6 or earlier", sdk)
	}
	model, _ := r.d.Getprop("ro.product.device")
	return fmt.Sprintf("%s, SDK %s, %s", model, sdk, r.d.Serial()), false, nil
}

// checkPermissive refuses a device that did not boot permissive. Setting it here would hide a flash
// that did not take: writing to rootfs needs it, and so does every boot after this one.
func checkPermissive(r *run) (string, bool, error) {
	mode, err := r.d.Shell("getenforce")
	if err != nil {
		return "", false, err
	}
	if mode = strings.TrimSpace(mode); !strings.EqualFold(mode, "permissive") {
		return "", false, fmt.Errorf("selinux is %s, want permissive", mode)
	}
	return mode, false, nil
}

func remountRW(r *run) (string, bool, error) {
	return "", false, r.d.Remount()
}

// remountRO puts the system partition back. adb only remounts one way, and no mount(8) on the device
// can do it, so this goes through the binary we just installed.
func remountRO(r *run) (string, bool, error) {
	// Every invocation of echod brings its components up and says so on stderr, which would land in
	// the middle of the progress display.
	out, err := r.d.Shell(layout.Binary + " tools remount ro 2>/dev/null")
	return strings.TrimSpace(out), false, err
}

func installBinary(r *run) (string, bool, error) {
	if _, err := r.d.Shell("mkdir -p " + layout.Dir); err != nil {
		return "", false, err
	}
	if err := clearTrial(r); err != nil {
		return "", false, err
	}
	if err := r.d.WriteFile(layout.Binary, r.cfg.Echod, 0o755); err != nil {
		return "", false, err
	}
	label, err := r.d.Label(layout.Binary)
	if err != nil {
		return "", false, err
	}
	return label, false, nil
}

// clearTrial throws away what a self-update left open. Installing is a replacement, not an upgrade:
// the binary that opened the trial is being overwritten, so the boot hook must not put it back, and
// echod must not read the restart that follows as a trial that died.
func clearTrial(r *run) error {
	_, err := r.d.Shell(fmt.Sprintf("rm -f %s %s %s; setprop %s ''; setprop %s ''",
		layout.PrevBinary, layout.OldBinary, layout.UpdatingPath,
		layout.TrialProp, layout.RolledBackProp))
	return err
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

// takeOverService points init's ledcontroller at echod. Already pointing there is left alone rather
// than relinked: an identical write reported as work is what made every re-run look like it had
// changed something, and this is the step whose effect a reboot actually settles — init starts what the
// link points at.
func takeOverService(r *run) (string, bool, error) {
	if current, err := r.d.Shell("readlink " + layout.Service); err == nil {
		if strings.TrimSpace(current) == layout.Binary {
			return "already " + layout.Binary, true, nil
		}
	}

	if _, err := r.d.Shell(fmt.Sprintf("rm -f %s && ln -s %s %s", layout.Service, layout.Binary, layout.Service)); err != nil {
		return "", false, err
	}
	target, err := r.d.Shell("readlink " + layout.Service)
	if err != nil {
		return "", false, err
	}

	r.reboot = true
	return strings.TrimSpace(target), false, nil
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
