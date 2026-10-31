package layout

import (
	"fmt"
	"os"
	"strings"
)

const (
	Dir      = "/system/app/echod"
	Binary   = Dir + "/echod"
	Surface  = Dir + "/echolocal-surface"
	Camera   = Dir + "/echolocal-camera"
	CamShim  = Dir + "/libecholocal-camshim.so"
	StateDir = "/data/misc/echolocal"
	KeyPath  = StateDir + "/psk"
	NamePath = StateDir + "/name"

	CrashPath = StateDir + "/crash"

	CastCredentialsPath = StateDir + "/cast-credentials.json"
	CastBundlePath      = StateDir + "/cast-bundle.json"
	CastAuthorityPath   = StateDir + "/cast-authority.json"
	BondsPath           = StateDir + "/bonds.json"
	TesseraDir          = StateDir

	CastAppDir      = StateDir + "/cast"
	CastEngineDir   = StateDir + "/engine"
	CastEngineState = StateDir + "/engine-state"

	FBDevice = "/dev/graphics/fb0"

	Service        = "echod"
	SurfaceService = "echolocal_sf"
	InitRC         = "/system/etc/init/echolocal.rc"

	CameraService       = "echolocal_cam"
	CameraServerService = "cameraserver"
	CameraSocket        = "/dev/socket/echolocal-camera"

	PrevBinary = StateDir + "/echod.prev"
	OldBinary  = StateDir + "/echod.old"

	SystemPrevBinary = Binary + ".prev"
	SystemOldBinary  = Binary + ".old"

	AsideBinary = Binary + ".aside"

	UpdatingPath = StateDir + "/updating"
)

// Both call ledctrl, which waits on a binder service echod does not publish.
const (
	StartAnimation = "/system/bin/start_animation.sh"
	StopAnimation  = "/system/bin/stop_animation.sh"
)

// system_file has no domain transition rule, leaving echod in init's own domain.
const OurLabel = "u:object_r:system_file:s0"

const (
	StartedProp = "echolocal.started"
	StateProp   = "echolocal.state"

	TrialProp = "echolocal.trial"

	RolledBackProp = "echolocal.rolledback"
)

const (
	WifiDir     = "/data/misc/wifi"
	WifiConf    = WifiDir + "/wpa_supplicant.conf"
	WifiBgscan  = "simple:30:-65:300"
	WifiSockets = WifiDir + "/sockets"
	WifiIface   = "wlan0"
	WifiUser    = 1010
)

// LogTag is echod's logcat tag: `adb logcat -s echolocal`.
const LogTag = "echolocal"

// The ESPHome native API port Home Assistant expects.
const Port = 6053

// Amazon's firewall.sh runs this script if it exists, after building its allowlist and flushing INPUT.
const FirewallHook = "/system/bin/greengrass_firewall.sh"

// The length limit the ESPHome API imposes on a node name.
const MaxNodeName = 31

const (
	Manufacturer = "EchoLocal"
	Platform     = "echolocal"
)

// idme is a kernel interface, readable before wlan0 exists and without /data.
const MACPath = "/proc/idme/mac_addr"

const StatePath = StateDir + "/state.json"

const RuntimePath = StateDir + "/runtime.json"

const ReleasesPath = StateDir + "/releases.json"

const ModelDir = StateDir + "/models"

const SpeechHelper = StateDir + "/bin/echolocal-asr"

const PryonDir = ModelDir + "/pryon"

const GeminiDir = ModelDir + "/gemini"

const PryonHelper = StateDir + "/bin/echolocal-pryon"

const AirPlayHelper = StateDir + "/bin/echolocal-airplay"

const TesseraHelper = StateDir + "/bin/echolocal-tessera"

const TesseraFirmwareDir = StateDir + "/tessera"

const TesseraPort = 6054

const AirPlayKey = StateDir + "/airplay.pem"

const RecordingDir = StateDir + "/recordings"

const ToneDir = StateDir + "/tones"

const TempDir = StateDir + "/tmp"

// idme writes twelve hex digits with no separators.
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

func FactoryMAC() (string, error) {
	raw, err := os.ReadFile(MACPath)
	if err != nil {
		return "", fmt.Errorf("reading the factory MAC: %w", err)
	}
	mac := MAC(string(raw))
	if mac == "" {
		return "", fmt.Errorf(
			"%s holds %q, which is not an address",
			MACPath,
			strings.TrimSpace(string(raw)),
		)
	}
	return mac, nil
}

// idme fields are written at manufacture and never change.
func Idme(name string) string {
	raw, err := os.ReadFile("/proc/idme/" + name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

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
