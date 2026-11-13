package board

import (
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/asp"
)

// Radar is the 2nd-generation Echo.
var Radar = Board{
	Device:       "radar_puffin",
	Codename:     "radar",
	Slotted:      true,
	SystemAsRoot: true,
	SignalsBoot:  true,
	Model:        "Echo 2nd gen (radar)",
	DefaultName:  "Echo",
	Caps:         Ring | MuteLight | HeadphoneJack,
	NativeAPI:    25,
	RadioZone:    "mtktswmt",

	// radar ships no EQ_90.cfg, and 2048-tap filters.
	Tuning: asp.Files{
		EQ: []asp.Step{
			{UpTo: 0.3, Name: "EQ_30.cfg"},
			{UpTo: 0.4, Name: "EQ_40.cfg"},
			{UpTo: 0.5, Name: "EQ_50.cfg"},
			{UpTo: 0.6, Name: "EQ_60.cfg"},
			{UpTo: 0.7, Name: "EQ_70.cfg"},
			{UpTo: 0.8, Name: "EQ_80.cfg"},
			{UpTo: 1.0, Name: "EQ_100.cfg"},
		},
		Limiter: "MBCL.cfg",
		Taps:    2048,
	},

	Service:     "/system/bin/ledcontroller",
	ServiceName: "ledcontroller",
	StockLabel:  "u:object_r:ledd_exec:s0",
	BootHooks:   []string{layout.StartAnimation, layout.StopAnimation},
}
