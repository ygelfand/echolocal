package layout

import "testing"

func TestMAC(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0A1B2C3D4E5F", "0a:1b:2c:3d:4e:5f"},
		{"0A1B2C3D4E5F\n", "0a:1b:2c:3d:4e:5f"},
		{"0a:1b:2c:3d:4e:5f", "0a:1b:2c:3d:4e:5f"},
		{"000000000000", ""},
		{"00:00:00:00:00:00", ""},
		{"0A1B2C3D4E", ""},
		{"0A1B2C3D4E5F60", ""},
		{"not an address", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := MAC(tt.in); got != tt.want {
			t.Errorf("MAC(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestColor(t *testing.T) {
	// Real serial format, fabricated tails: black units read G090LF…, white G090L9…, and the tail is
	// the part that identifies a unit, so it is zeroed rather than real.
	const (
		blackSerial = "G090LF0000000000"
		whiteSerial = "G090L90000000000"
	)

	tests := []struct {
		name                     string
		productID2, serial, want string
	}{
		{"both say black", "0", blackSerial, "black"},
		{"both say white", "20", whiteSerial, "white"},
		{"white high code", "2020", whiteSerial, "white"},
		{"trailing space", "20 ", whiteSerial, "white"},

		{"fields disagree", "0", whiteSerial, "unknown"},
		{"nothing to read", "", "", "unknown"},
		{"productid2 only", "0", "", "black"},
		{"serial only", "", whiteSerial, "white"},
		{"serial too short", "", "G090L", "unknown"},
	}
	for _, tt := range tests {
		if got := Color(tt.productID2, tt.serial); got != tt.want {
			t.Errorf("%s: Color(%q, %q) = %q, want %q", tt.name, tt.productID2, tt.serial, got, tt.want)
		}
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Kitchen", "kitchen"},
		{"Living Room", "living-room"},
		{"kitchen", "kitchen"},
		{"  Front   Porch  ", "front-porch"},
		{"Kid's Room", "kid-s-room"},
		{"Echo Dot A1B2C3", "echo-dot-a1b2c3"},
		{"café", "caf"},
		{"---", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Slug(tt.in); got != tt.want {
			t.Errorf("Slug(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
