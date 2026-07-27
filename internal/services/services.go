package services

import (
	"fmt"
	"strings"

	"github.com/ygelfand/echolocal/internal/device"
)

// Hide hides every package in Hidden and force-stops any that are running, reporting what it
// changed. Hiding blocks future launches but does not touch a running process, so being hidden
// already is no reason to skip the stop — only being dead is.
//
// am force-stop costs about 0.6s each, a binder call into system_server, so the running set is
// read once from ps rather than stopping all fifteen blindly.
func Hide(d *device.Device) (hid, stopped []string, err error) {
	current, err := HiddenOn(d)
	if err != nil {
		return nil, nil, err
	}
	running, err := runningPackages(d)
	if err != nil {
		return nil, nil, err
	}

	for _, p := range Hidden {
		if !current[p.Name] {
			if _, err := d.Shell("pm hide " + p.Name); err != nil {
				return hid, stopped, fmt.Errorf("hiding %s: %w", p.Name, err)
			}
			hid = append(hid, p.Name)
		}
		if running[p.Name] {
			if _, _, err := d.ShellCode("am force-stop " + p.Name); err != nil {
				return hid, stopped, fmt.Errorf("stopping %s: %w", p.Name, err)
			}
			stopped = append(stopped, p.Name)
		}
	}
	return hid, stopped, nil
}

// runningPackages reports which packages have a live process. Android names a package's process
// after the package, with a :suffix for extra processes.
func runningPackages(d *device.Device) (map[string]bool, error) {
	out, err := d.Shell("ps")
	if err != nil {
		return nil, err
	}

	set := make(map[string]bool)
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := fields[len(fields)-1]
		if pkg, _, found := strings.Cut(name, ":"); found {
			name = pkg
		}
		set[name] = true
	}
	return set, nil
}

// Unhide restores every package in Hidden.
func Unhide(d *device.Device) (changed []string, err error) {
	current, err := HiddenOn(d)
	if err != nil {
		return nil, err
	}

	for _, p := range Hidden {
		if !current[p.Name] {
			continue
		}
		if _, err := d.Shell("pm unhide " + p.Name); err != nil {
			return changed, fmt.Errorf("unhiding %s: %w", p.Name, err)
		}
		changed = append(changed, p.Name)
	}
	return changed, nil
}

// HiddenOn reports which packages are currently hidden. There is no command that lists them, so
// it is the difference between the visible packages and every installed one.
func HiddenOn(d *device.Device) (map[string]bool, error) {
	visible, err := packageSet(d, "pm list packages")
	if err != nil {
		return nil, err
	}
	all, err := packageSet(d, "pm list packages -u")
	if err != nil {
		return nil, err
	}

	hidden := make(map[string]bool)
	for name := range all {
		if !visible[name] {
			hidden[name] = true
		}
	}
	return hidden, nil
}

func packageSet(d *device.Device, cmd string) (map[string]bool, error) {
	out, err := d.Shell(cmd)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool)
	for line := range strings.SplitSeq(out, "\n") {
		if name := strings.TrimPrefix(strings.TrimSpace(line), "package:"); name != "" {
			set[name] = true
		}
	}
	return set, nil
}
