// Package bootimg identifies the boot image echod needs and the devices it may be written to.
//
// echod cannot run on a stock boot image. In init's domain, socket creation is denied silently — a
// dontaudit rule hides the AVC — so the ESPHome listener fails with EACCES, and only permissive
// fixes it. The image we ship carries androidboot.selinux=permissive on its kernel cmdline, and it
// also carries an adbd that honours ro.secure=0, which is what makes adb root work afterwards.
// Amazon's own adbd drops privileges regardless of that property.
package bootimg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// Partition is the only partition that may be written, and the size it must measure.
//
// The name matters more than it looks. On this firmware the amonet bootloader and the kernel disagree
// about names: to fastboot, boot_a is a 110 MB partition, while to the kernel boot_a is the 16 MB one.
// Only the _x form means the same partition to both, so that is the only name used here, and the size
// is asserted before writing in case a name ever resolves somewhere else.
const (
	Partition = "boot_a_x"
	Node      = "/dev/block/bootdevice/by-name/" + Partition

	// PartitionSize is what boot_a_x measures on a biscuit. A resolved node of 115343360 bytes is an
	// amonet partition and must never be written with a boot image.
	PartitionSize = 16777216
)

// Image is the boot image we ship, and what a device has to be for it to fit.
type Image struct {
	// SHA256 and Size identify the file, so a truncated or substituted image is refused before a
	// device is touched.
	SHA256 string
	Size   int64

	// Cmdline is the substring that has to appear in the kernel cmdline. It is the reason this image
	// exists rather than an incidental property.
	Cmdline string

	// Firmware are the Fire OS versions the image is known good on, matched against the leading
	// version of ro.build.version.incremental.
	//
	// The whole property reads like 272.6.8.0_user_680767620, and only the version in front is worth
	// comparing: what follows is a build identifier that says nothing about whether this ramdisk
	// belongs on that system partition, and pinning it would refuse devices for no reason.
	Firmware []string

	// Device is the ro.product.device it belongs to.
	Device string
}

// Ours describes images/echolocal-boot.img.
//
// Its ramdisk keeps MTK section headers, so anything unpacking it has to skip the 512-byte ROOTFS
// header before the gzip. Its sepolicy differs from what is on a stock biscuit, which does not matter
// while the device runs permissive but does mean this is a separate build rather than a stock image
// with a patched cmdline: 7621064 of its 7696384 bytes differ from the boot partition it replaces.
var Ours = Image{
	SHA256:   "373727a90314328ede65585103552c2d0e2908ac43a1a596f9650421fa0700ab",
	Size:     7696384,
	Cmdline:  "androidboot.selinux=permissive",
	Firmware: []string{"272.6.8.0"},
	Device:   "biscuit",
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

// Supports reports whether the image belongs on a device. Both the device and the firmware version
// are checked, because the ramdisk pairs with a particular system partition — the right image on the
// wrong firmware boots someone else's ramdisk against this system.
//
// build is ro.build.version.incremental, of which only the leading version is compared.
func (i Image) Supports(device, build string) error {
	if device != i.Device {
		return fmt.Errorf("bootimg: this is a %s image and the device is %q", i.Device, device)
	}
	if firmware := Firmware(build); !slices.Contains(i.Firmware, firmware) {
		return fmt.Errorf("bootimg: firmware %q is not one this image is known good on (%v)",
			firmware, i.Firmware)
	}
	return nil
}

// Firmware is the Fire OS version in a ro.build.version.incremental value, without the build
// identifier that follows it.
func Firmware(build string) string {
	version, _, _ := strings.Cut(build, "_")
	return version
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
