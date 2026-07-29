// Package layout is the on-device layout echoctl writes and echod reads. Both binaries have to
// agree on every name here, so they come from one place rather than being repeated.
package layout

import "strings"

// Where echod and its state live. /system is read-only once installed, so anything written
// after install goes on /data.
const (
	Dir      = "/system/app/echod"
	Binary   = Dir + "/echod"
	StateDir = "/data/misc/echolocal"
	KeyPath  = StateDir + "/psk"
	NamePath = StateDir + "/name"
)

// echod runs as Amazon's ledcontroller service: taking over the definition removes the only
// other writer of the LED ring and gets us init's supervision.
const (
	Service      = "/system/bin/ledcontroller"
	BackupSuffix = ".orig"
	Backup       = Service + BackupSuffix
	ServiceName  = "ledcontroller"
)

// AnimationScripts are the boot animation wrappers init runs; both call ledctrl, which waits on
// a binder service echod does not publish.
var AnimationScripts = []string{
	"/system/bin/start_animation.sh",
	"/system/bin/stop_animation.sh",
}

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
const (
	ModelDir         = StateDir + "/models"
	DefaultWakeModel = ModelDir + "/hey_jarvis.tflite"
)

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
