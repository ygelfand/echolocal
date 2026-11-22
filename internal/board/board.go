package board

import (
	"strings"

	"github.com/ygelfand/echolocal/internal/lib/asp"
)

type Cap uint32

const (
	Ring Cap = 1 << iota

	Panel

	Wifi

	Shutter

	Camera

	MuteLight

	MicMutesCamera

	HeadphoneJack

	MFPGatedAmp
)

type Key string

const (
	Mute       Key = "mute"
	VolumeDown Key = "volume_down"
	VolumeUp   Key = "volume_up"
	Action     Key = "action"
	Power      Key = "power"
)

type Tessera struct {
	Board         string
	Columns, Rows int
}

type Board struct {
	NativeAPI int

	// Device is ro.product.device; a biscuit after the Fire OS 6 OTA reports biscuit_puffin.
	Device string

	Codename string

	Slotted bool

	SystemAsRoot bool

	SignalsBoot bool

	BootHooks []string

	Model string

	DefaultName string

	Caps Cap

	Keys map[uint16]Key

	Touch []string

	MuteDir string

	// A screen capture reads back through the same rotation it was drawn with.
	PanelRotation int

	Diagonal float64

	UISize string

	Tessera Tessera

	RadioZone string

	SecureDecoders map[string]string

	MaxFPS int

	Tuning asp.Files

	Stereo bool

	DecodeWidth, DecodeHeight int

	CameraWidth, CameraHeight int
	CameraSizes               []string
	SubWidth                  int

	Service     string
	ServiceName string
	StockLabel  string

	color func(idme func(string) string) string
}

func (b Board) Has(c Cap) bool { return b.Caps&c != 0 }

func (b Board) String() string {
	if b.Codename == "" {
		return "unknown"
	}
	return b.Codename
}

func (b Board) NameFromMAC(mac string) string {
	var hex strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(mac)) {
		if r >= '0' && r <= '9' || r >= 'A' && r <= 'F' {
			hex.WriteRune(r)
		}
	}
	s := hex.String()
	if len(s) < 6 {
		return b.DefaultName
	}
	return b.DefaultName + " " + s[len(s)-6:]
}
