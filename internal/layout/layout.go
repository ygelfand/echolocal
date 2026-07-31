// Package layout is the on-device layout echoctl writes and echod reads. Both binaries have to
// agree on every name here, so they come from one place rather than being repeated.
package layout

import (
	"strings"
	"syscall"
)

// Where echod and its state live. /system is read-only once installed, so anything written
// after install goes on /data.
const (
	Dir      = "/system/app/echod"
	Binary   = Dir + "/echod"
	StateDir = "/data/misc/echolocal"
	KeyPath  = StateDir + "/psk"
	NamePath = StateDir + "/name"

	// PrevBinary is the binary an update replaced, kept until the new one has proved itself. Its
	// presence at boot is what says a trial never finished, so nothing may leave one lying around.
	// OldBinary is where a proven update files it, one generation back.
	PrevBinary = Binary + ".prev"
	OldBinary  = Binary + ".old"

	// UpdatingPath holds the version being tried, so a rollback can say which one it took out. It is
	// under /data because the boot hook reads it after a restore has already remounted /system back to
	// read-only.
	UpdatingPath = StateDir + "/updating"
)

// echod runs as Amazon's ledcontroller service: taking over the definition removes the only
// other writer of the LED ring and gets us init's supervision.
const (
	Service      = "/system/bin/ledcontroller"
	BackupSuffix = ".orig"
	Backup       = Service + BackupSuffix
	ServiceName  = "ledcontroller"
)

// The boot animation wrappers init runs; both call ledctrl, which waits on a binder service echod does
// not publish. StartAnimation runs on the way up and StopAnimation once Android reports the boot
// finished, which is the difference that decides what may go in them.
const (
	StartAnimation = "/system/bin/start_animation.sh"
	StopAnimation  = "/system/bin/stop_animation.sh"
)

var AnimationScripts = []string{StartAnimation, StopAnimation}

// SELinux labels. A service's domain comes from the label of the file init execs, and
// system_file has no transition rule, which leaves echod in init's own domain.
const (
	OurLabel   = "u:object_r:system_file:s0"
	StockLabel = "u:object_r:ledd_exec:s0"
)

// Properties echod publishes about itself. Started is an uptime, which only moves forward
// within a boot, so a changed value means a new process.
const (
	StartedProp = "echolocal.started"
	StateProp   = "echolocal.state"

	// TrialProp marks that a process this boot already took an update and has not committed it.
	// Deliberately not a persist property: it has to survive init restarting echod, which is what
	// makes a second attempt recognisable, and it has to be forgotten across a reboot, which is what
	// gives the boot hook its turn.
	TrialProp = "echolocal.trial"

	// RolledBackProp is set by the boot hook when it puts the previous binary back, so the failure
	// reaches Home Assistant instead of only logcat.
	RolledBackProp = "echolocal.rolledback"
)

// LogTag is echod's logcat tag: `adb logcat -s echolocal`.
const LogTag = "echolocal"

// Port is the ESPHome native API port Home Assistant expects.
const Port = 6053

// FirewallHook is a script Amazon's firewall.sh runs if it exists, after building its allowlist
// and after flushing INPUT. Occupying it is how our port stays open across every invocation
// without editing their script. Greengrass is not installed and nothing else references it.
const FirewallHook = "/system/bin/greengrass_firewall.sh"

// MaxNodeName is the length limit the ESPHome API imposes on a node name.
const MaxNodeName = 31

// Hardware identity, as Home Assistant shows it in the device panel.
const (
	Manufacturer = "EchoLocal"
	Model        = "Echo Dot 2 (biscuit)"
	Board        = "biscuit"
	Platform     = "echolocal"

	// DefaultName is the fallback display name when a device has none recorded.
	DefaultName = "Echo Dot"
)

// MACPath is the address the factory recorded, which the Wi-Fi driver takes when it comes up. idme
// is a kernel interface, so it reads this early in boot, before wlan0 exists and without /data.
const MACPath = "/proc/idme/mac_addr"

// StatePath holds echod's runtime settings.
const StatePath = StateDir + "/state.json"

// Wake word models live in /data, not /system: Home Assistant can offer new ones at runtime and
// /system is mounted read-only.
const ModelDir = StateDir + "/models"

// Free is the space left on the filesystem holding path, in bytes.
func Free(path string) (int64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, err
	}
	return int64(fs.Bavail) * int64(fs.Bsize), nil
}

// MAC normalizes an address into the form Home Assistant compares against, and reports "" for
// anything that would not identify a device. idme writes twelve hex digits with no separators.
func MAC(raw string) string {
	var digits strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' {
			digits.WriteRune(r)
		}
	}

	s := digits.String()
	if len(s) != 12 || s == "000000000000" {
		return ""
	}

	var mac strings.Builder
	for i := 0; i < len(s); i += 2 {
		if i > 0 {
			mac.WriteByte(':')
		}
		mac.WriteString(s[i : i+2])
	}
	return mac.String()
}

// NameFromMAC builds the fallback display name, unique per device.
func NameFromMAC(mac string) string {
	var hex strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(mac)) {
		if r >= '0' && r <= '9' || r >= 'A' && r <= 'F' {
			hex.WriteRune(r)
		}
	}
	s := hex.String()
	if len(s) < 6 {
		return DefaultName
	}
	return DefaultName + " " + s[len(s)-6:]
}

// Slug turns a display name into a node name: an mDNS hostname, and the prefix Home Assistant
// builds entity ids from. "Living Room" becomes "living-room".
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}
