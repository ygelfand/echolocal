package board

import (
	"github.com/ygelfand/libcountertop/pkg/media/h264"

	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/asp"
)

var Show5Tuning = asp.Files{
	EQ: []asp.Step{
		{UpTo: 0.4, Name: "EQ_40.cfg"},
		{UpTo: 0.6, Name: "EQ_60.cfg"},
		{UpTo: 0.8, Name: "EQ_80.cfg"},
		{UpTo: 1.0, Name: "EQ_100.cfg"},
	},
	Limiter: "MBCL_default.cfg",
}

// Checkers is the 1st-generation Echo Show 5.
var Checkers = Board{
	NativeAPI:     25,
	Device:        "checkers",
	Codename:      "checkers",
	Model:         "Echo Show 5 1st gen (checkers)",
	DefaultName:   "Echo Show 5",
	Caps:          Panel | Wifi | Shutter | Camera | MicMutesCamera | HeadphoneJack,
	PanelRotation: 270,
	UISize:        "mini",
	Diagonal:      5.5,
	Tessera:       Tessera{Board: "sunton8048s070", Columns: 4, Rows: 2},
	DecodeWidth:   1920,
	DecodeHeight:  1088,
	Tuning:        Show5Tuning,
	SecureDecoders: map[string]string{
		"video/avc":  "OMX.MTK.VIDEO.DECODER.AVC.secure",
		"video/hevc": "OMX.MTK.VIDEO.DECODER.HEVC.secure",
	},

	CameraWidth: 1280, CameraHeight: 720,
	CameraSizes:   []string{"1280x720", "960x480", "864x480", "640x480"},
	CameraProfile: h264.Baseline,

	// The mute button reports KEY_POWER, on a node of its own called "gating".
	Keys:    map[uint16]Key{116: Mute},
	Touch:   []string{"goodix-ts"},
	MuteDir: "/sys/devices/platform/amazon-gating",

	Service:     layout.Binary,
	ServiceName: layout.Service,
}
