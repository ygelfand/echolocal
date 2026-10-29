package board

import (
	"strconv"
	"strings"

	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/asp"
)

// Biscuit is the 2nd-generation Echo Dot.
var Biscuit = Board{
	Device:       "biscuit_puffin",
	Codename:     "biscuit",
	Slotted:      true,
	SystemAsRoot: true,
	SignalsBoot:  true,
	Model:        "Echo Dot 2 (biscuit)",
	DefaultName:  "Echo Dot",
	Caps:         Ring | MuteLight | HeadphoneJack,
	NativeAPI:    25,
	RadioZone:    "mtktswmt",
	Tuning: asp.Files{
		EQ: []asp.Step{
			{UpTo: 0.5, Name: "EQ_50.cfg"},
			{UpTo: 0.6, Name: "EQ_60.cfg"},
			{UpTo: 0.7, Name: "EQ_70.cfg"},
			{UpTo: 0.8, Name: "EQ_80.cfg"},
			{UpTo: 0.9, Name: "EQ_90.cfg"},
			{UpTo: 1.0, Name: "EQ_100.cfg"},
		},
		Limiter: "MBCL.cfg",
	},

	Service:     "/system/bin/ledcontroller",
	ServiceName: "ledcontroller",
	StockLabel:  "u:object_r:ledd_exec:s0",
	BootHooks:   []string{layout.StartAnimation, layout.StopAnimation},

	color: func(idme func(string) string) string {
		return biscuitColor(idme("productid2"), idme("serial"))
	},
}

// idme productid2 is 0 on black, nonzero on white; serials run G090LF… black, G090L9… white.
func biscuitColor(productID2, serial string) string {
	prod := ColorUnknown
	if n, err := strconv.Atoi(strings.TrimSpace(productID2)); err == nil {
		if n == 0 {
			prod = ColorBlack
		} else {
			prod = ColorWhite
		}
	}

	ser := ColorUnknown
	if s := strings.TrimSpace(serial); len(s) > 5 {
		switch s[5] {
		case 'F', 'f':
			ser = ColorBlack
		case '9':
			ser = ColorWhite
		}
	}

	switch {
	case prod == ColorUnknown:
		return ser
	case ser == ColorUnknown:
		return prod
	case prod == ser:
		return prod
	default:
		return ColorUnknown
	}
}
