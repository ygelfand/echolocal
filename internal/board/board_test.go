package board

import "testing"

func TestBiscuitColor(t *testing.T) {
	const (
		blackSerial = "G090LF0000000000"
		whiteSerial = "G090L90000000000"
	)

	tests := []struct {
		name                     string
		productID2, serial, want string
	}{
		{"both say black", "0", blackSerial, ColorBlack},
		{"both say white", "20", whiteSerial, ColorWhite},
		{"white high code", "2020", whiteSerial, ColorWhite},
		{"trailing space", "20 ", whiteSerial, ColorWhite},

		{"fields disagree", "0", whiteSerial, ColorUnknown},
		{"nothing to read", "", "", ColorUnknown},
		{"productid2 only", "0", "", ColorBlack},
		{"serial only", "", whiteSerial, ColorWhite},
		{"serial too short", "", "G090L", ColorUnknown},
	}
	for _, tt := range tests {
		idme := func(field string) string {
			if field == "productid2" {
				return tt.productID2
			}
			return tt.serial
		}
		if got := Biscuit.Color(idme); got != tt.want {
			t.Errorf(
				"%s: Color(%q, %q) = %q, want %q",
				tt.name,
				tt.productID2,
				tt.serial,
				got,
				tt.want,
			)
		}
	}
}

func TestAnUndecodedBoardReportsUnknown(t *testing.T) {
	idme := func(string) string { return "whatever some other board writes here" }
	if got := Crown.Color(idme); got != ColorUnknown {
		t.Errorf("Crown.Color() = %q, want %q", got, ColorUnknown)
	}
}

func TestOnlyBoardsWithADeviceCanMatch(t *testing.T) {
	read := map[string]string{
		"biscuit":  "biscuit_puffin",
		"checkers": "checkers",
		"cronos":   "cronos",
		"crown":    "crown",
		"athena":   "athena",
		"rook":     "rook",
		"radar":    "radar_puffin",
	}

	for _, b := range All {
		if b.Device == "" {
			continue
		}
		if want := read[b.Codename]; b.Device != want {
			t.Errorf("%s has ro.product.device %q, want %q", b, b.Device, want)
		}
	}

	for _, name := range Intended() {
		if b, ok := For(name); ok {
			t.Errorf("For(%q) matched %s, which has no ro.product.device", name, b)
		}
	}
}

func TestFor(t *testing.T) {
	if b, ok := For("biscuit_puffin"); !ok || b.Codename != "biscuit" {
		t.Errorf(`For("biscuit_puffin") = %v, %v; want biscuit`, b, ok)
	}
	if b, ok := For("  biscuit_puffin\n"); !ok || b.Codename != "biscuit" {
		t.Errorf("For did not trim what a device said: %v, %v", b, ok)
	}
	// biscuit's recovery reports biscuit.
	if b, ok := For("biscuit"); !ok || b.Codename != "biscuit" {
		t.Errorf(`For("biscuit") = %v, %v; want biscuit`, b, ok)
	}
	if b, ok := For("radar_puffin"); !ok || b.Codename != "radar" {
		t.Errorf(`For("radar_puffin") = %v, %v; want radar`, b, ok)
	}
	if b, ok := For("radar"); !ok || b.Codename != "radar" {
		t.Errorf(`For("radar") = %v, %v; want radar`, b, ok)
	}
	if _, ok := For("sailfish"); ok {
		t.Error("For matched a device this build has never heard of")
	}
}

func TestUnsupportedBoardsClaimNothing(t *testing.T) {
	if Biscuit.ServiceName == "" {
		t.Error("biscuit names no service for echod to be installed as")
	}
	supported := map[string]bool{
		Biscuit.Device: true, Checkers.Device: true, Cronos.Device: true,
		Crown.Device: true, Athena.Device: true, Rook.Device: true, Radar.Device: true,
	}

	for _, b := range All {
		if supported[b.Device] {
			continue
		}
		if b.Service != "" || b.ServiceName != "" || b.StockLabel != "" {
			t.Errorf("%s names a service to take over", b)
		}
		if b.Caps != 0 {
			t.Errorf("%s claims capabilities %b", b, b.Caps)
		}
	}
}

func TestNameFromMAC(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0a:1b:2c:3d:4e:5f", "Echo Dot 3D4E5F"},
		{"0A1B2C3D4E5F", "Echo Dot 3D4E5F"},
		{"", "Echo Dot"},
		{"0a:1b", "Echo Dot"},
	}
	for _, tt := range tests {
		if got := Biscuit.NameFromMAC(tt.in); got != tt.want {
			t.Errorf("NameFromMAC(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
