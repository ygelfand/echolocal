package layout

import "testing"

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
