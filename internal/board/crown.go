package board

import (
	"github.com/ygelfand/libcountertop/pkg/media/h264"

	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/asp"
)

// Crown is the 1st-generation Echo Show 8.
var Crown = Board{
	NativeAPI:     25,
	Device:        "crown",
	Codename:      "crown",
	Model:         "Echo Show 8 1st gen (crown)",
	DefaultName:   "Echo Show 8",
	Caps:          Panel | Wifi | Shutter | Camera | MicMutesCamera | HeadphoneJack,
	PanelRotation: 270,
	UISize:        "mini",
	Diagonal:      8.0,
	Tessera:       Tessera{Board: "jc8012p4a1", Columns: 6, Rows: 4},
	DecodeWidth:   1920,
	DecodeHeight:  1088,
	Tuning: asp.Files{
		EQ: []asp.Step{
			{UpTo: 0.3, Name: "EQ_30.cfg"},
			{UpTo: 0.5, Name: "EQ_50.cfg"},
			{UpTo: 0.7, Name: "EQ_70.cfg"},
			{UpTo: 1.0, Name: "EQ_100.cfg"},
		},
		Limiter: "MBCL_default.cfg",
	},
	Stereo: true,
	SecureDecoders: map[string]string{
		"video/avc":  "OMX.MTK.VIDEO.DECODER.AVC.secure",
		"video/hevc": "OMX.MTK.VIDEO.DECODER.HEVC.secure",
	},
	Keys:    map[uint16]Key{116: Mute},
	Touch:   []string{"fts_ts"},
	MuteDir: "/sys/devices/platform/amazon-gating",

	CameraWidth: 1280, CameraHeight: 720,
	CameraSizes:   []string{"1280x720", "960x480", "864x480", "640x480"},
	CameraProfile: h264.Baseline,

	Service:     layout.Binary,
	ServiceName: layout.Service,
}
