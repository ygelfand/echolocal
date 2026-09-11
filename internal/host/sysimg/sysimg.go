// Package sysimg describes the changes echod needs on the system partition.
//
// The boot image alone is not enough. This is a system-as-root device: the root filesystem lives on
// the system partition, so default.prop, sbin/adbd and the fstab are all there rather than in the boot
// ramdisk. Amazon's adbd drops privileges however ro.secure is set, so without replacing it there is no
// root adb and nothing else in the install can run.
package sysimg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/ygelfand/echolocal/internal/host/bootimg"
)

// Partition is the system slot for a slot suffix, the half of an A/B device that is running.
func Partition(slot string) string { return "system" + slot }

// Node is that partition's device node.
func Node(slot string) string { return bootimg.ByName + Partition(slot) }

// VerityOffset is where the system partition's dm-verity metadata starts. The fstab that replaces the
// stock one drops the verify flag, and this overwrites the metadata it would have checked; a modified
// system with either one left in place does not boot.
const VerityOffset = 798945280

// The first four bytes at VerityOffset. Stock is the verity header's own magic; Disabled is what is
// written over it. Anything else is not the image these offsets were measured on.
const (
	StockMagic    = "01b001b0"
	DisabledMagic = "564f4646" // "VOFF"
)

// DisabledBytes is what is written over the verity header's magic.
var DisabledBytes = []byte("VOFF")

// The property that stops Fire OS taking an update over this install. An OTA writes the other slot and
// switches to it, which replaces the system this pairs with and leaves the device unusable to us.
const (
	OTAKey   = "ro.build.version.number"
	OTAValue = "4503599628273540"
)

// Patch is one file written onto the mounted system partition, and what it has to be. It replaces the
// contents of a file that is already there, so the mode and owner are whatever the stock file carried.
type Patch struct {
	Path   string
	SHA256 string
	Size   int64
}

var (
	DefaultProp = Patch{
		Path:   "default.prop",
		SHA256: "9090f348d70531d9f9794ae3036481f4508ea20dbedd703465338f1ce4169a1d",
		Size:   579,
	}
	Fstab = Patch{
		Path:   "fstab.mt8163",
		SHA256: "43d9039682c9af8c9f3b59eceb6e1baaf03abb5d575123df071f6d3f5961d881",
		Size:   1470,
	}
)

// Adbd is the four bytes that stop the stock adbd dropping privileges: movs r0, #1; bx lr, which turns
// the check into one that always passes. Patching rather than replacing the binary means a build this
// was not derived from is refused instead of overwritten.
var Adbd = struct {
	Path          string
	Offset        int64
	Before, After []byte
}{
	Path:   "sbin/adbd",
	Offset: 105460,
	Before: []byte{0x10, 0xb5, 0x02, 0x20},
	After:  []byte{0x01, 0x20, 0x70, 0x47},
}

// Verify reports whether these bytes are the file described.
func (p Patch) Verify(data []byte) error {
	if int64(len(data)) != p.Size {
		return fmt.Errorf("sysimg: %s is %d bytes, want %d", p.Path, len(data), p.Size)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != p.SHA256 {
		return fmt.Errorf("sysimg: %s hashes to %s, want %s", p.Path, got, p.SHA256)
	}
	return nil
}

// Verity reads what the first four bytes at VerityOffset mean.
func Verity(magic []byte) (disabled bool, err error) {
	switch hex.EncodeToString(magic) {
	case DisabledMagic:
		return true, nil
	case StockMagic:
		return false, nil
	}
	return false, fmt.Errorf("sysimg: no verity metadata at %d, found %x", VerityOffset, bytes.TrimRight(magic, "\x00"))
}
