package cpu

import "testing"

func TestParsePresent(t *testing.T) {
	tests := []struct {
		in   string
		want int
		bad  bool
	}{
		{in: "0-3\n", want: 4},
		{in: "0-1", want: 2},
		{in: "0", want: 1},
		{in: "0-1,2-3", want: 4},
		{in: "", bad: true},
		{in: "none", bad: true},
	}
	for _, tt := range tests {
		got, err := parsePresent(tt.in)
		if tt.bad {
			if err == nil {
				t.Errorf("parsePresent(%q) = %d, want an error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePresent(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parsePresent(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
