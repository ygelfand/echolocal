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

const ByName = "/dev/block/platform/bootdevice/by-name/"

// FireOS 6 boots from a 110 MB pair; the original 16 MB pair is renamed boot_a_x and boot_b_x.
var PartitionSizes = []int64{16777216, 115343360}

func KnownPartition(size int64) bool { return slices.Contains(PartitionSizes, size) }

func Partition(slot string) string { return "boot" + slot }

func Node(slot string) string { return ByName + Partition(slot) }

type Image struct {
	SHA256 string
	Size   int64

	Cmdline string

	Build int64

	Device string

	File string

	URL string
}

const Tag = "boot-images"

const downloads = "https://github.com/ygelfand/echolocal/releases/download/" + Tag + "/"

// Its ramdisk keeps MTK headers: skip the 512-byte ROOTFS header before the gzip.
var Biscuit = Image{
	SHA256:  "7f12e1522211d2e1adfc0164a5c2381b8819f44099732bf919b12c23e41e7dd1",
	Size:    9678848,
	Cmdline: "androidboot.selinux=permissive",
	Build:   13121734532,
	Device:  "biscuit_puffin",

	File: "boot/echolocal-boot-biscuit.img",
	URL:  downloads + "echolocal-boot-biscuit.img",
}

var Checkers = Image{
	SHA256:  "634e567500b1ec9712fc66fb1ca07dac0b36f352a7164fede32da739d1409d42",
	Size:    8210432,
	Cmdline: "androidboot.selinux=permissive",
	Build:   13222531716,
	Device:  "checkers",

	File: "boot/echolocal-boot-checkers.img",
	URL:  downloads + "echolocal-boot-checkers.img",
}

var Cronos = Image{
	SHA256:  "7de1246bf8501087fbd134257c6d24390bc6a2cb3b9e013fafebdfdc1540105d",
	Size:    8691712,
	Cmdline: "androidboot.selinux=permissive",
	Build:   13222531716,
	Device:  "cronos",

	File: "boot/echolocal-boot-cronos.img",
	URL:  downloads + "echolocal-boot-cronos.img",
}

var Crown = Image{
	SHA256:  "798997a7b65e16ea348b255b30316bd72f32cb31f42e89388884cba97d291f61",
	Size:    8443904,
	Cmdline: "androidboot.selinux=permissive",
	Build:   13222531716,
	Device:  "crown",

	File: "boot/echolocal-boot-crown.img",
	URL:  downloads + "echolocal-boot-crown.img",
}

// Fire OS build 12718777476's boot image, with androidboot.selinux=permissive on the cmdline.
var Radar = Image{
	SHA256:  "d43e09549323d86c4bd20d88f1efd37642122ed8b5ee740f338ab95978936451",
	Size:    9762304,
	Cmdline: "androidboot.selinux=permissive",
	Build:   12718777476,
	Device:  "radar_puffin",

	File: "boot/echolocal-boot-radar.img",
	URL:  downloads + "echolocal-boot-radar.img",
}

var Images = []Image{Biscuit, Checkers, Cronos, Crown, Radar}

func (i Image) Asset() string {
	at := strings.LastIndex(i.URL, "/")
	if at < 0 {
		return ""
	}
	return i.URL[at+1:]
}

var magic = []byte("ANDROID!")

const (
	cmdlineOffset = 64
	cmdlineSize   = 512
)

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

func (i Image) Supports(device, build string) (string, error) {
	if device != i.Device {
		return "", fmt.Errorf("bootimg: this is a %s image and the device is %q", i.Device, device)
	}

	on, err := strconv.ParseInt(strings.TrimSpace(build), 10, 64)
	if err != nil {
		return fmt.Sprintf(
			"build %q cannot be ranked against the %d this image was tested on",
			build,
			i.Build,
		), nil
	}
	if on > i.Build {
		return fmt.Sprintf(
			"build %d is newer than the %d this image was tested on",
			on,
			i.Build,
		), nil
	}
	return "", nil
}

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
