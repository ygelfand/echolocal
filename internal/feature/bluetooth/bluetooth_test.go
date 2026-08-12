package bluetooth

import (
	"net"
	"slices"
	"testing"
)

func TestBeaconAdvertisement(t *testing.T) {
	got, minor := beaconAdvertisement([]net.IP{
		net.ParseIP("fd00::1"),
		net.ParseIP("192.168.1.42"),
	})
	want := []byte{
		0x02, 0x01, 0x06,
		0x1a, 0xff, 0x4c, 0x00, 0x02, 0x15,
		0x9c, 0x5f, 0xa6, 0xf1, 0x91, 0xc4, 0x4f, 0x56,
		0xbb, 0x9f, 0xd9, 0x2a, 0xcf, 0xd9, 0xd4, 0x0b,
		0x00, 0x01, 0x00, 0x2a, 0xc5,
	}

	if minor != 42 {
		t.Errorf("minor = %d, want 42", minor)
	}
	if !slices.Equal(got, want) {
		t.Errorf("advertisement = %x, want %x", got, want)
	}
}

func TestBeaconAdvertisementNeedsIPv4(t *testing.T) {
	got, minor := beaconAdvertisement([]net.IP{net.ParseIP("fd00::1")})
	if got != nil || minor != -1 {
		t.Errorf("advertisement = %x, minor = %d; want nil, -1", got, minor)
	}
}
