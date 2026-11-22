package board

import "github.com/ygelfand/echolocal/internal/layout"

// Athena is the 2nd-generation Echo Show 8.
var Athena = Board{
	NativeAPI:    28,
	Device:       "athena",
	Codename:     "athena",
	SystemAsRoot: true,
	Model:        "Echo Show 8 2nd gen (athena)",
	DefaultName:  "Echo Show 8",
	Caps:         Panel | Wifi | Shutter | Camera | MicMutesCamera,
	UISize:       "mini",
	Diagonal:     8.0,
	Tessera:      Tessera{Board: "jc8012p4a1", Columns: 6, Rows: 4},
	DecodeWidth:  1920,
	DecodeHeight: 1088,
	Tuning:       Crown.Tuning,
	Stereo:       true,
	SecureDecoders: map[string]string{
		"video/avc":  "OMX.MTK.VIDEO.DECODER.AVC.secure",
		"video/hevc": "OMX.MTK.VIDEO.DECODER.HEVC.secure",
	},
	Keys:    map[uint16]Key{116: Mute},
	Touch:   []string{"fts_ts", "goodix-ts"},
	MuteDir: "/sys/devices/platform/gpio-privacy",

	CameraWidth: 1280, CameraHeight: 720,
	CameraSizes: []string{"1920x1080", "1280x720", "640x480"},
	SubWidth:    640,

	Service:     layout.Binary,
	ServiceName: layout.Service,
}
