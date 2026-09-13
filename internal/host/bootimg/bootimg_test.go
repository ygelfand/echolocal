package bootimg

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// made builds an image with the same shape as a real one — magic, a cmdline at the header offset,
// padded to size — so Verify can be exercised without the 7 MB file.
func made(size int64, cmdline string) ([]byte, Image) {
	data := make([]byte, size)
	copy(data, magic)
	copy(data[cmdlineOffset:], cmdline)

	sum := sha256.Sum256(data)
	return data, Image{
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    size,
		Cmdline: "androidboot.selinux=permissive",
		Build:   13121734532,
		Device:  "biscuit_puffin",
	}
}

func TestVerifyAcceptsWhatItDescribes(t *testing.T) {
	data, img := made(4096, "bootopt=64S3 androidboot.selinux=permissive")

	if err := img.Verify("test", data); err != nil {
		t.Fatalf("Verify on its own image: %v", err)
	}
}

// Each property is checked separately so a failure says which one, rather than reporting every wrong
// file as a hash mismatch.
func TestVerifyRefusesEachWay(t *testing.T) {
	data, img := made(4096, "bootopt=64S3 androidboot.selinux=permissive")

	for _, tc := range []struct {
		name  string
		data  []byte
		about string
	}{
		{"short", data[:2048], "bytes"},
		{"long", append(append([]byte{}, data...), 0), "bytes"},
		{"wrong magic", func() []byte {
			d := append([]byte{}, data...)
			copy(d, "XNDROID!")
			return d
		}(), "does not start with"},
		{"one byte flipped", func() []byte {
			d := append([]byte{}, data...)
			d[3000] ^= 0xFF
			return d
		}(), "hashes to"},
	} {
		err := img.Verify(tc.name, tc.data)
		if err == nil {
			t.Errorf("%s: accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.about) {
			t.Errorf("%s: %v, want it to mention %q", tc.name, err, tc.about)
		}
	}
}

// An image whose cmdline lacks permissive is the dangerous case: it is a valid boot image that boots
// enforcing, so echod comes up unable to listen and nothing about it looks wrong.
func TestVerifyRefusesAnEnforcingCmdline(t *testing.T) {
	data, img := made(4096, "bootopt=64S3,32N2,64N2")

	err := img.Verify("enforcing", data)
	if err == nil {
		t.Fatal("accepted an image with no permissive cmdline")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("%v, want it to say what is missing", err)
	}
}

// The device is the half that refuses. A build this image was never tested on is installable, since
// Amazon ships new ones and waiting for a release here would leave them uninstallable.
func TestSupportsRefusesTheDeviceAndOnlyRemarksOnTheBuild(t *testing.T) {
	_, img := made(4096, "androidboot.selinux=permissive")

	for _, tc := range []struct {
		device, build       string
		ok, sayingSomething bool
	}{
		{"biscuit_puffin", "13121734532", true, false},
		{"biscuit_puffin", "13121734533", true, true},
		{"biscuit_puffin", "13121734531", true, false},
		{"biscuit_puffin", "", true, true},
		{"biscuit_puffin", "272.6.8.0_user_680767620", true, true},
		{"biscuit", "13121734532", false, false},
		{"tank", "13121734532", false, false},
	} {
		said, err := img.Supports(tc.device, tc.build)
		if (err == nil) != tc.ok {
			t.Errorf("Supports(%q, %q) = %v, want ok=%t", tc.device, tc.build, err, tc.ok)
		}
		if err == nil && (said != "") != tc.sayingSomething {
			t.Errorf("Supports(%q, %q) said %q, want anything=%t", tc.device, tc.build, said, tc.sayingSomething)
		}
	}
}

// A device that names no slot has no partition to write, and "boot" on its own is not one here.
func TestPartitionIsTheSlot(t *testing.T) {
	if got := Partition("_b"); got != "boot_b" {
		t.Errorf("Partition(_b) = %q", got)
	}
	if got := Node("_a"); got != ByName+"boot_a" {
		t.Errorf("Node(_a) = %q", got)
	}
}

func TestCmdline(t *testing.T) {
	data, _ := made(4096, "bootopt=64S3 androidboot.selinux=permissive")
	if got := Cmdline(data); got != "bootopt=64S3 androidboot.selinux=permissive" {
		t.Errorf("Cmdline = %q", got)
	}
	if got := Cmdline(data[:16]); got != "" {
		t.Errorf("Cmdline of a truncated header = %q, want empty", got)
	}
}

// The partition is written with dd in fixed blocks, and the count comes from the image size, so a size
// that is not a whole number of blocks would read back short and fail a write that was fine.
func TestOursFitsHowItIsWritten(t *testing.T) {
	if Ours.Size%512 != 0 {
		t.Errorf("image size %d is not a multiple of 512", Ours.Size)
	}
	for _, size := range PartitionSizes {
		if Ours.Size >= size {
			t.Errorf("image is %d bytes and the partition is %d", Ours.Size, size)
		}
	}
}

// The shipped image is committed, so this always runs: it is what keeps the table and the file from
// drifting apart, and a missing image means a build that cannot produce a release.
func TestShippedImageIsWhatWeSayItIs(t *testing.T) {
	const path = "../assets/boot.img"

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the shipped image: %v", err)
	}
	if err := Ours.Verify(path, data); err != nil {
		t.Error(err)
	}
}
