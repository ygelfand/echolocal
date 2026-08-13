package bluetooth

import (
	"slices"
	"testing"
)

func TestBeaconAdvertisement(t *testing.T) {
	got := beaconAdvertisement(0xbeef)
	want := []byte{
		0x02, 0x01, 0x06,
		0x1a, 0xff, 0x4c, 0x00, 0x02, 0x15,
		0x9c, 0x5f, 0xa6, 0xf1, 0x91, 0xc4, 0x4f, 0x56,
		0xbb, 0x9f, 0xd9, 0x2a, 0xcf, 0xd9, 0xd4, 0x0b,
		0x00, 0x01, 0xbe, 0xef, 0xc5,
	}

	if !slices.Equal(got, want) {
		t.Errorf("advertisement = %x, want %x", got, want)
	}
}

func TestBeaconAdvertisementRejectsInvalidMinor(t *testing.T) {
	if got := beaconAdvertisement(-1); got != nil {
		t.Errorf("advertisement = %x, want nil", got)
	}
}

func TestBeaconMinor(t *testing.T) {
	if got := beaconMinor("74:c2:46:12:be:ef"); got != 0xbeef {
		t.Errorf("minor = %d, want %d", got, 0xbeef)
	}
	if got := beaconMinor("not a MAC"); got != -1 {
		t.Errorf("minor = %d, want -1", got)
	}
}
