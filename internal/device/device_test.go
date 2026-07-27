package device

import "testing"

func TestSplitRC(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantOut string
		wantRC  int
		wantErr bool
	}{
		{
			name:    "success with CRLF output",
			raw:     "uid=0(root)\r\n" + rcMarker + "0\r\n",
			wantOut: "uid=0(root)",
		},
		{
			name:    "failure carries its status",
			raw:     "/nope: No such file or directory\r\n" + rcMarker + "1\r\n",
			wantOut: "/nope: No such file or directory",
			wantRC:  1,
		},
		{
			name:    "no output at all",
			raw:     rcMarker + "1\r\n",
			wantOut: "",
			wantRC:  1,
		},
		{
			name:    "marker text appearing in output does not confuse the parse",
			raw:     "echo " + rcMarker + "9\r\n" + rcMarker + "0\r\n",
			wantOut: "echo " + rcMarker + "9",
			wantRC:  0,
		},
		{
			name:    "missing marker is an error, not a silent zero",
			raw:     "truncated output\r\n",
			wantErr: true,
		},
		{
			name:    "unparseable status is an error",
			raw:     "out\r\n" + rcMarker + "banana\r\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, rc, err := splitRC(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got out=%q rc=%d", out, rc)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out != tt.wantOut {
				t.Errorf("output = %q, want %q", out, tt.wantOut)
			}
			if rc != tt.wantRC {
				t.Errorf("rc = %d, want %d", rc, tt.wantRC)
			}
		})
	}
}

func TestQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/system/bin/ledcontroller", `'/system/bin/ledcontroller'`},
		{"u:object_r:system_file:s0", `'u:object_r:system_file:s0'`},
		{"it's", `'it'\''s'`},
	}
	for _, tt := range tests {
		if got := quote(tt.in); got != tt.want {
			t.Errorf("quote(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}
