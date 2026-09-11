// Package services is the Amazon software an install takes out of the way, and how.
//
// Fire OS 6 has no Android framework on this device — no app_process, no package manager, five
// packages and none of them running. Everything Amazon runs is a native init service, so getting them
// out of the way means marking them disabled in the init rc that defines them: a disabled service does
// not start with its class and waits for a ctl.start that never comes.
package services

import "strings"

// Disabled is what an install stops from starting. Three kinds: the ones holding hardware echod needs,
// the ones that talk to Amazon, and the ones that exist to watch the first two.
//
// What is deliberately left running: the wifi chain (wifisvc, netmgrd, p2p_supplicant, wmt_launcher),
// thermal management (acethermald and acepowerd, which applies its policy) and the ace_eventmgr
// dispatcher both of those talk over, along with the usual Android plumbing.
var Disabled = []string{
	// Hold the audio devices and the bluetooth controller echod drives itself.
	"mixer",
	"BTSinkPlayer",
	"btmanagerd",
	"blemesh_service",

	// Alexa and the services around it. puffin is the Alexa client itself.
	"puffin",
	"puffinmrmd",
	"ahe",
	"smarthomed",
	"commsd",
	"shs",
	"amakit_server",
	"tokend",
	"provisionerd",
	"oobed_on_boot",
	"UdssCampSvc",
	"assetmgrd",
	"dacd",
	"uxeventd",

	"adepd",

	// fosflags strips adb out of persist.sys.usb.config on every boot, which is what leaves a device
	// with no adb at all. It acts on the bits in /proc/idme/fos_flags, and with those at zero — which
	// is how a retail device ships, and they cannot be set without fastboot — that is the only thing
	// it does: console, ramdump, verbosity and dexopt are all gated on bits that are not set.
	"fosflags",

	// Metrics, crash upload and over-the-air updates.
	"ace_metricd",
	"aceusagestatd",
	"usagestat_protod",
	"acedropboxd",
	"minerva_service",
	"trackerd",
	"logmgr",
	"ace_coex_metric",
	"otad",
	"ace_otad",
	"halo-bssid-scan",

	// Buttons, input and sensors, which echod reads for itself.
	"acebuttond",
	"aceinputmanager",
	"ace_sensorsd",

	// Watch and restart Alexa, and measure it.
	"perfmonitord",
	"perfrecoveryd",

	"factory-reset",
	"avahi-daemon",
	"neo-coordinator",
	"neo-init",
	"fireos-dha",
}

// Enabled are the services an install leaves to init to start.
//
// dhcpcd ships disabled and no rc starts it: Amazon's wifi daemon starts it by name for connections it
// makes itself, and echoctl makes one by talking to wpa_supplicant directly. Started at boot it waits,
// and takes a lease when the interface associates.
var Enabled = []string{"dhcpcd-wlan0"}

// Enable removes the disabled keyword from the named services, so init starts them with their class.
func Enable(rc string, names map[string]bool) (string, []string) {
	lines := strings.Split(rc, "\n")

	var (
		out     = make([]string, 0, len(lines))
		changed []string
		inside  string
	)

	for _, line := range lines {
		if name, ok := serviceName(line); ok {
			inside = ""
			if names[name] {
				inside = name
			}
			out = append(out, line)
			continue
		}

		if inside != "" {
			switch trimmed := strings.TrimSpace(line); {
			case trimmed == "":
			case trimmed == "disabled":
				changed = append(changed, inside)
				continue
			case !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t"):
				inside = ""
			}
		}
		out = append(out, line)
	}

	return strings.Join(out, "\n"), changed
}

// DisabledSet and EnabledSet are the lists as lookups.
func DisabledSet() map[string]bool { return set(Disabled) }
func EnabledSet() map[string]bool  { return set(Enabled) }

func set(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

// Disable stops the named services starting, and reports what it changed. Anything already in the
// state we want is left alone, so an install can be run again.
//
// Two things start a service and both have to go. The disabled keyword only covers the first:
//
//   - its class, which init starts as a group
//   - an explicit "start name" in an on-block, which runs whatever the keyword says
func Disable(rc string, names map[string]bool) (string, []string) {
	lines := strings.Split(rc, "\n")

	var (
		out     = make([]string, 0, len(lines)+len(names))
		changed []string
	)

	for i := 0; i < len(lines); i++ {
		if name, ok := started(lines[i]); ok && names[name] {
			out = append(out, "#"+lines[i])
			changed = append(changed, "start "+name)
			continue
		}

		out = append(out, lines[i])

		name, ok := serviceName(lines[i])
		if !ok || !names[name] {
			continue
		}

		// The command can be wrapped over several lines, and the keyword has to follow all of it.
		for strings.HasSuffix(strings.TrimRight(lines[i], " \t"), `\`) && i+1 < len(lines) {
			i++
			out = append(out, lines[i])
		}

		if blockHas(lines[i+1:], "disabled") {
			continue
		}
		out = append(out, "    disabled")
		changed = append(changed, name)
	}

	return strings.Join(out, "\n"), changed
}

// Domain is what the service echod takes over is given to run in. init refuses to start a service
// whose executable produces no domain transition — "does not have a SELinux domain defined" — and our
// binary is labelled system_file, which produces none. Naming the domain outright skips that, and this
// one is unconfined, which echod needs before it can make the device permissive at all.
const Domain = "u:r:su:s0"

// AsRoot makes a service run as root in that domain: the user and group Amazon declares are dropped,
// and a seclabel is added. echod needs root for the audio devices, the GPIO lines and the remount an
// update does.
func AsRoot(rc, name string) (string, []string) {
	lines := strings.Split(rc, "\n")

	var (
		out     = make([]string, 0, len(lines)+1)
		changed []string
		inside  bool
	)

	for i := 0; i < len(lines); i++ {
		if at, ok := serviceName(lines[i]); ok {
			out = append(out, lines[i])

			if inside = at == name; !inside {
				continue
			}
			if !blockHas(lines[i+1:], "seclabel") {
				out = append(out, "    seclabel "+Domain)
				changed = append(changed, "seclabel "+Domain)
			}
			continue
		}

		if inside {
			switch fields := strings.Fields(lines[i]); {
			case len(fields) == 0:
			case fields[0] == "user", fields[0] == "group":
				changed = append(changed, strings.TrimSpace(lines[i]))
				continue
			case !strings.HasPrefix(lines[i], " ") && !strings.HasPrefix(lines[i], "\t"):
				inside = false
			}
		}
		out = append(out, lines[i])
	}

	return strings.Join(out, "\n"), changed
}

// started is the name a "start <name>" command inside an on-block names.
func started(line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) != 2 || fields[0] != "start" {
		return "", false
	}
	return fields[1], true
}

// serviceName is the name a "service <name> <command>" line declares.
func serviceName(line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "service" {
		return "", false
	}
	return fields[1], true
}

// blockHas reports whether the option lines that follow already carry the keyword. A block runs until
// the first line that is not indented, which is where the next service or action begins.
func blockHas(rest []string, keyword string) bool {
	for _, line := range rest {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
		case !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t"):
			return false
		case trimmed == keyword, strings.HasPrefix(trimmed, keyword+" "):
			return true
		}
	}
	return false
}
