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
