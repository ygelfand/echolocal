package installer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ygelfand/libcountertop/pkg/host/adb"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/host/profile"
	"github.com/ygelfand/echolocal/internal/layout"
)

type Status int

const (
	Running Status = iota
	Done
	Skipped
	Failed
)

type Event struct {
	Step   int
	Total  int
	Name   string
	Status Status
	Detail string
	Err    error
}

type Reporter func(Event)

type Config struct {
	Echod []byte

	Name string

	ZeroPSK bool

	BootImage     []byte
	BootImageFrom string
	Profile       profile.Profile

	Approved bool

	Reflash bool
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
	{"hide Amazon packages", hidePackages},
	{"clear the saved usb config", clearUSBConfig},
	{"install echod", installBinary},
	{"install the echod service", installService},
	{"run echod as root", serviceAsRoot},
	{"take over the service", takeOverService},
	{"disable the boot animation", disableBootAnimation},
	{"open the API port", installFirewallHook},
	{"bring up wifi", bringUpWifi},
	{"stop service", stopService},
	{"remount /system ro", remountRO},
	{"start echod", startService},
}

var restartSteps = []step{
	{"stop echod", stopService},
	{"start echod", startService},
}

type run struct {
	d   *adb.Device
	cfg Config

	ctx context.Context

	state state

	on board.Board

	reboot bool
}

func Install(ctx context.Context, d *adb.Device, cfg Config, report Reporter) (bool, error) {
	r := &run{d: d, cfg: cfg, ctx: ctx}
	err := execute(ctx, steps, r, report)
	return r.reboot, err
}

func FlashBoot(ctx context.Context, d *adb.Device, cfg Config, report Reporter) error {
	return execute(ctx, flashSteps, &run{d: d, cfg: cfg, ctx: ctx}, report)
}

func Restart(ctx context.Context, d *adb.Device, report Reporter) error {
	return execute(ctx, restartSteps, &run{d: d}, report)
}

func execute(ctx context.Context, steps []step, r *run, report Reporter) error {
	for i, s := range steps {
		if i > 0 {
			time.Sleep(time.Second)
		}
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
	model, _ := r.d.Getprop("ro.product.device")
	p, err := profile.For(model)
	if err != nil {
		return "", false, err
	}

	if sdk != p.SDK() {
		return "", false, fmt.Errorf("device reports SDK %s, %s", sdk, p.SDKRefusal())
	}
	return fmt.Sprintf("%s, SDK %s, %s", model, sdk, r.d.Serial()), false, nil
}

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

// adb only remounts read-write, and no mount(8) on the device can remount the root.
func remountRO(r *run) (string, bool, error) {
	out, err := r.d.Shell(layout.Binary + " tools remount ro 2>/dev/null")
	if err != nil && heldOpen(r) {
		r.reboot = true
		return "read-only after the reboot", true, nil
	}
	return strings.TrimSpace(out), false, err
}

// ext4 refuses a read-only remount while a deleted file on it is still open.
func heldOpen(r *run) bool {
	_, err := r.d.Shell(
		`for f in /proc/[0-9]*/fd/*; do readlink "$f"; done 2>/dev/null | grep -q '^/system/.* (deleted)$'`,
	)
	return err == nil
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

func clearTrial(r *run) error {
	_, err := r.d.Shell(fmt.Sprintf("rm -f %s %s %s %s %s %s; setprop %s ''; setprop %s ''",
		layout.PrevBinary, layout.OldBinary, layout.SystemPrevBinary, layout.SystemOldBinary,
		layout.AsideBinary, layout.UpdatingPath,
		layout.TrialProp, layout.RolledBackProp))
	return err
}

func takeOverService(r *run) (string, bool, error) {
	service := r.board().Service
	if service == layout.Binary {
		return "init starts echod itself", true, nil
	}

	if current, err := r.d.Shell("readlink " + service); err == nil {
		if strings.TrimSpace(current) == layout.Binary {
			return "already " + layout.Binary, true, nil
		}
	}

	if _, err := r.d.Shell(
		fmt.Sprintf("rm -f %s && ln -s %s %s", service, layout.Binary, service),
	); err != nil {
		return "", false, err
	}
	target, err := r.d.Shell("readlink " + service)
	if err != nil {
		return "", false, err
	}

	r.reboot = true
	return strings.TrimSpace(target), false, nil
}

// init publishes a transient "stopping" before the process is reaped.
func stopService(r *run) (string, bool, error) {
	name := r.board().ServiceName

	if err := r.d.Setprop("ctl.stop", name); err != nil {
		return "", false, err
	}

	var state string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		if state, err = r.d.Getprop("init.svc." + name); err != nil {
			return "", false, err
		}
		if state == "stopped" {
			return state, false, nil
		}
		if state == "" {
			return "not defined until the next boot", true, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", false, fmt.Errorf("service is %q 10s after ctl.stop, want stopped", state)
}

// ctl.start returns before the process has run.
func startService(r *run) (string, bool, error) {
	name := r.board().ServiceName
	before, _ := r.d.Getprop(layout.StartedProp)

	if err := r.d.Setprop("ctl.start", name); err != nil {
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

	// init refuses a service whose block it has not read, and says so only in its own log.
	state, _ := r.d.Getprop("init.svc." + name)
	r.reboot = true
	return fmt.Sprintf("needs a reboot (init.svc.%s=%s)", name, state), true, nil
}
