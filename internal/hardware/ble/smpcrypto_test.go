package ble

import (
	"encoding/hex"
	"testing"
)

// The sample data is the specification's, in air order as Linux's SMP self-test carries it.

func b16(t *testing.T, s string) [16]byte {
	var out [16]byte
	if n, err := hex.Decode(out[:], []byte(s)); err != nil || n != 16 {
		t.Fatalf("bad 16-byte hex %q", s)
	}
	return out
}

func b32(t *testing.T, s string) [32]byte {
	var out [32]byte
	if n, err := hex.Decode(out[:], []byte(s)); err != nil || n != 32 {
		t.Fatalf("bad 32-byte hex %q", s)
	}
	return out
}

func TestCMAC(t *testing.T) {
	// RFC 4493, written most significant first: reverse into air order and back.
	k := [16]byte(reversed(mustHex(t, "2b7e151628aed2a6abf7158809cf4f3c")))
	for _, tc := range []struct{ msg, mac string }{
		{"", "bb1d6929e95937287fa37d129b756746"},
		{"6bc1bee22e409f96e93d7e117393172a", "070a16b46b4d4144f79bdd9dd04a287c"},
		{"6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e5130c81c46a35ce411",
			"dfa66747de9ae63030ca32611497c827"},
	} {
		got := aesCMAC(k, reversed(mustHex(t, tc.msg)))
		if hex.EncodeToString(reversed(got[:])) != tc.mac {
			t.Errorf("cmac(%s) = %x, want %s", tc.msg, reversed(got[:]), tc.mac)
		}
	}
}

func mustHex(t *testing.T, s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestC1(t *testing.T) {
	var k [16]byte
	r := b16(t, "e02e70c64e2788630e6fad5621d58357")
	preq := [7]byte{0x01, 0x01, 0x00, 0x00, 0x10, 0x07, 0x07}
	pres := [7]byte{0x02, 0x03, 0x00, 0x00, 0x08, 0x00, 0x05}
	ra := [6]byte{0xb6, 0xb5, 0xb4, 0xb3, 0xb2, 0xb1}
	ia := [6]byte{0xa6, 0xa5, 0xa4, 0xa3, 0xa2, 0xa1}
	if got := c1(k, r, preq, pres, 1, ia, 0, ra); got != b16(t, "863bf1bec54da7d2ea888987ef3f1e1e") {
		t.Errorf("c1 = %x", got)
	}
}

func TestS1(t *testing.T) {
	var k, r1, r2 [16]byte
	copy(r1[:], mustHex(t, "8877665544332211"))
	copy(r2[:], mustHex(t, "00ffeeddccbbaa99"))
	if got := s1(k, r1, r2); got != b16(t, "62a06d79ae16425b9bf4b0e8f0e11f9a") {
		t.Errorf("s1 = %x", got)
	}
}

func TestF4(t *testing.T) {
	u := b32(t, "e69d350e480103ccdbfdf4ac1191f4efb9a5f9e9a7832c5e2cbe97f2d203b020")
	v := b32(t, "fdc57ff449dd4f6bfb7c9df1c29acb592ae7d4eefbfc0a909abbf6323d8b1855")
	x := b16(t, "abae2b71ecb2ffff3e7377d15484cbd5")
	if got := f4(u, v, x, 0); got != b16(t, "2d8774a9bea1edf11cbda907f116c9f2") {
		t.Errorf("f4 = %x", got)
	}
}

func TestF5(t *testing.T) {
	w := b32(t, "98a6bf73f3348d86f166f8b4136b79999b7d390aa610103405adc857a33402ec")
	n1 := b16(t, "abae2b71ecb2ffff3e7377d15484cbd5")
	n2 := b16(t, "cfc43dfff78365216e5fa725cce7e8a6")
	a1 := [7]byte{0xce, 0xbf, 0x37, 0x37, 0x12, 0x56, 0x00}
	a2 := [7]byte{0xc1, 0xcf, 0x2d, 0x70, 0x13, 0xa7, 0x00}
	mac, ltk := f5(w, n1, n2, a1, a2)
	if ltk != b16(t, "380a7594b522059823cdd76911798669") {
		t.Errorf("ltk = %x", ltk)
	}
	if mac != b16(t, "206e63ce206a3ffd024a08a176f16529") {
		t.Errorf("mackey = %x", mac)
	}
}

func TestF6(t *testing.T) {
	w := b16(t, "206e63ce206a3ffd024a08a176f16529")
	n1 := b16(t, "abae2b71ecb2ffff3e7377d15484cbd5")
	n2 := b16(t, "cfc43dfff78365216e5fa725cce7e8a6")
	r := b16(t, "c80f2d0cd242da0854bb53b43b34a312")
	a1 := [7]byte{0xce, 0xbf, 0x37, 0x37, 0x12, 0x56, 0x00}
	a2 := [7]byte{0xc1, 0xcf, 0x2d, 0x70, 0x13, 0xa7, 0x00}
	if got := f6(w, n1, n2, r, [3]byte{0x02, 0x01, 0x01}, a1, a2); got != b16(t, "618f95da090b6cd2c5e8d09c9873c4e3") {
		t.Errorf("f6 = %x", got)
	}
}
