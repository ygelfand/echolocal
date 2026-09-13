// Package bootimg identifies the boot image echod needs and the devices it may be written to.
//
// The image we ship carries androidboot.selinux=permissive on its kernel cmdline, which is what makes
// a build that honours it permissive. A user build's init compiles that check out, and there the flash
// stage's patches to the system partition are what do it.
package bootimg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ByName is where partitions are named rather than numbered.
const ByName = "/dev/block/platform/bootdevice/by-name/"

// PartitionSizes are what a boot slot measures, asserted before writing in case a name ever resolves
// somewhere else. Writing a boot image into a partition of another size is how a device stops booting.
//
// There are two layouts. A device that took the FireOS 6 OTA was repartitioned: its boot slots are the
// 110 MB pair at the end of the eMMC, and the original 16 MB pair is still there renamed to boot_a_x
// and boot_b_x. A device unlocked before that OTA never took it, since amonet's decoys are what stop
// one applying, so it keeps the 16 MB pair as its boot slots.
var PartitionSizes = []int64{16777216, 115343360}

// KnownPartition reports whether a boot slot of this size is one we will write.
func KnownPartition(size int64) bool { return slices.Contains(PartitionSizes, size) }

// Partition is the boot slot for a slot suffix, which is ro.boot.slot_suffix: the slot the device is
// running, and so the one that has to carry the image for the next boot to use it.
func Partition(slot string) string { return "boot" + slot }

// Node is that partition's device node.
func Node(slot string) string { return ByName + Partition(slot) }

// Image is the boot image we ship, and what a device has to be for it to fit.
type Image struct {
	// SHA256 and Size identify the file, so a truncated or substituted image is refused before a
	// device is touched.
	SHA256 string
	Size   int64

	// Cmdline is the substring that has to appear in the kernel cmdline. It is the reason this image
	// exists rather than an incidental property.
	Cmdline string

	// Build is ro.build.version.incremental as it read on the firmware this image was tested against.
	// A device on a later one is said so and carried on with: Amazon ships new builds, and refusing
	// them would leave every one needing a release here before it could be installed at all.
	Build int64

	// Device is the ro.product.device it belongs to.
	Device string
}

// Ours describes images/echolocal-boot.img.
//
// Its ramdisk keeps MTK section headers, so anything unpacking it has to skip the 512-byte ROOTFS
// header before the gzip.
var Ours = Image{
	SHA256:  "7f12e1522211d2e1adfc0164a5c2381b8819f44099732bf919b12c23e41e7dd1",
	Size:    9678848,
	Cmdline: "androidboot.selinux=permissive",
	Build:   13121734532,
	Device:  "biscuit_puffin",
}

// magic is what every Android boot image starts with.
var magic = []byte("ANDROID!")

// cmdlineOffset is where the kernel command line sits in the boot header, and cmdlineSize is how much
// room it has.
const (
	cmdlineOffset = 64
	cmdlineSize   = 512
)

// Verify reports whether these bytes are the image described. Every property is checked, not only the
// hash, so a mismatch says which one failed: a wrong hash is a different file, a missing cmdline is an
// image that boots enforcing and leaves echod unable to listen. from names the source in errors.
func (i Image) Verify(from string, data []byte) error {
	if int64(len(data)) != i.Size {
		return fmt.Errorf("bootimg: %s is %d bytes, want %d", from, len(data), i.Size)
	}
	if !bytes.HasPrefix(data, magic) {
		return fmt.Errorf("bootimg: %s does not start with %s", from, magic)
	}

	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != i.SHA256 {
		return fmt.Errorf("bootimg: %s hashes to %s, want %s", from, got, i.SHA256)
	}
	if cmd := Cmdline(data); !bytes.Contains([]byte(cmd), []byte(i.Cmdline)) {
		return fmt.Errorf("bootimg: %s has cmdline %q, which is missing %q", from, cmd, i.Cmdline)
	}
	return nil
}

// Supports reports whether the image belongs on a device, and what is worth saying about one it was
// never tested against. The device is refused outright, since the ramdisk pairs with a particular
// system partition; a later build is only remarked on.
//
// build is ro.build.version.incremental.
func (i Image) Supports(device, build string) (string, error) {
	if device != i.Device {
		return "", fmt.Errorf("bootimg: this is a %s image and the device is %q", i.Device, device)
	}

	on, err := strconv.ParseInt(strings.TrimSpace(build), 10, 64)
	if err != nil {
		return fmt.Sprintf("build %q cannot be ranked against the %d this image was tested on", build, i.Build), nil
	}
	if on > i.Build {
		return fmt.Sprintf("build %d is newer than the %d this image was tested on", on, i.Build), nil
	}
	return "", nil
}

// Cmdline is the kernel command line stored in a boot image header.
func Cmdline(image []byte) string {
	if len(image) < cmdlineOffset+cmdlineSize {
		return ""
	}
	raw := image[cmdlineOffset : cmdlineOffset+cmdlineSize]
	if end := bytes.IndexByte(raw, 0); end >= 0 {
		raw = raw[:end]
	}
	return string(raw)
}
