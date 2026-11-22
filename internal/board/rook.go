package board

// Rook is the 1st-generation Echo Spot, running LineageOS.
var Rook = Board{
	Device:       "rook",
	Codename:     "rook",
	SystemAsRoot: true,
	Model:        "Echo Spot (rook)",
	DefaultName:  "Echo Spot",
	Caps:         Panel | Wifi,
	Touch:        []string{"goodix-ts"},
	Tessera:      Tessera{Board: "guition", Columns: 2, Rows: 3},

	Service:     "/system/app/echod/echod",
	ServiceName: "zygote",
}
