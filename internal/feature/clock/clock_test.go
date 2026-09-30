package clock

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// leaseWith writes a DHCP message naming the given time servers.
func leaseWith(t *testing.T, ips ...[4]byte) string {
	t.Helper()
	b := make([]byte, 236)
	b = binary.BigEndian.AppendUint32(b, 0x63825363)
	if len(ips) > 0 {
		b = append(b, 42, byte(4*len(ips)))
		for _, ip := range ips {
			b = append(b, ip[:]...)
		}
	}
	b = append(b, 255)
	p := filepath.Join(t.TempDir(), "lease")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCandidatesOrder(t *testing.T) {
	c := &Clock{
		leasePath:  leaseWith(t, [4]byte{192, 168, 15, 3}, [4]byte{192, 168, 4, 5}),
		configured: func() []string { return []string{"ntp.example.net", " 192.168.4.5 "} },
	}
	got := c.candidates()
	want := []source{
		{"192.168.15.3", "dhcp"},
		{"192.168.4.5", "dhcp"},
		{"ntp.example.net", "config"},
		{"pool.ntp.org", "default"},
		{"time.cloudflare.com", "default"},
		{"time.google.com", "default"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate %d is %v, want %v", i, got[i], want[i])
		}
	}
}

func TestCandidatesWithoutLease(t *testing.T) {
	c := &Clock{
		leasePath:  filepath.Join(t.TempDir(), "missing"),
		configured: func() []string { return nil },
	}
	got := c.candidates()
	if len(got) != len(fallback) || got[0].via != "default" {
		t.Fatalf("got %v", got)
	}
}
