// Package installer installs echod as the ledcontroller service.
package installer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ygelfand/echolocal/internal/android/services"
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

	// Pryon installs the wake-only privileged APK and the user-owned Android media bridge.
	// The Amazon libraries, APK and models stay on the attached Dot and are discovered there.
	Pryon        bool
	PryonAPK     []byte
	AndroidMedia []byte

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
	{"inspect Pryon firmware", inspectPryon},
	{"hide Amazon packages", hidePackages},
	{"gate Amazon init services", gateProps},
	{"device name", installName},
	{"encryption key", installKey},
	{"default wake words", installModels},
	{"install Android media bridge", installAndroidMedia},
	{"remount /system rw", remountRW},
	{"install Pryon companion", installPryonAPK},
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

var uninstallSteps = []step{
	{"stop echod", stopService},
	{"stop Pryon companion", stopPryon},
	{"remount /system rw", remountRW},
	{"remove Pryon companion", removePryonAPK},
	{"close the API port", removeFirewallHook},
	{"restore the boot animation", restoreBootAnimation},
	{"restore stock ledcontroller", restoreService},
	{"remove echod", removeBinary},
	{"remove Android media bridge", removeAndroidMedia},
	{"remount /system ro", remountRO},
	{"start ledcontroller", startStock},
}

type run struct {
	d   *device.Device
	cfg Config

	// ctx is the caller's, for the steps that wait on a device rebooting.
	ctx context.Context

	// state is what the device last said about itself. The flash stage reads it before deciding to
	// write anything and again afterwards to judge whether it worked.
	state state
	pryon pryonPaths

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
	if len(r.cfg.Echod) == 0 {
		return "", false, errors.New("no echod binary given")
	}
	if r.cfg.Pryon {
		if len(r.cfg.PryonAPK) == 0 {
			return "", false, errors.New("Pryon enabled but no companion APK was given")
		}
		if len(r.cfg.AndroidMedia) == 0 {
			return "", false, errors.New("Pryon enabled but no Android media helper was given")
		}
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

	// A package that was running has been stopped, but nothing here stops it from being started again
	// by whatever asked for it last. A boot with it hidden from the start is the state we are after.
	r.reboot = true
	return fmt.Sprintf("hid %d of %d, stopped %d", len(hid), len(services.Hidden), len(stopped)), false, nil
}

// gateProps stops init from starting the vendor services that hiding a package leaves stranded.
// The properties persist, so this lands on the next boot rather than now.
func gateProps(r *run) (string, bool, error) {
	changed, err := services.Gate(r.d)
	if err != nil {
		return "", false, err
	}
	if len(changed) == 0 {
		return fmt.Sprintf("%d already set", len(services.Gated)), true, nil
	}

	// init reads these when it starts a service, so nothing changes for the services already running.
	r.reboot = true
	return fmt.Sprintf("set %d of %d, effective next boot", len(changed), len(services.Gated)), false, nil
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
