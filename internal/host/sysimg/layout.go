package sysimg

import "github.com/ygelfand/echolocal/internal/host/bootimg"

type Layout struct {
	Partitions []Partition

	Policy string

	Patches []Patch

	Adbd BinPatch

	OTAKey, OTAValue string

	PropsPath string
	Props     map[string]string

	Gate []string

	ServiceRC string
}

func (l Layout) Empty() bool {
	return len(l.Partitions) == 0 && l.Policy == "" && len(l.Patches) == 0 &&
		l.Adbd.Path == "" && len(l.Props) == 0 && len(l.Gate) == 0 && l.ServiceRC == ""
}

type Partition struct {
	Name string

	Mount string

	VerityOffset int64
}

func (p Partition) Node(slot string) string { return bootimg.ByName + p.Name + slot }

type BinPatch struct {
	Path          string
	Offset        int64
	Before, After []byte
}

var Biscuit = Layout{
	Partitions: []Partition{{Name: "system", VerityOffset: VerityOffset}},
	Policy:     "sepolicy",
	Patches:    []Patch{DefaultProp, Fstab},
	Adbd:       Adbd,
	OTAKey:     OTAKey,
	OTAValue:   OTAValue,
}

var Radar = Layout{
	Partitions: []Partition{{Name: "system", VerityOffset: VerityOffset}},
	Policy:     "sepolicy",
	Patches:    []Patch{Fstab},
	Adbd:       Adbd,
	OTAKey:     OTAKey,
	OTAValue:   OTAValue,
	PropsPath:  "default.prop",
	Props: map[string]string{
		"ro.secure":              "0",
		"ro.adb.secure":          "0",
		"ro.debuggable":          "1",
		"persist.sys.usb.config": "mtp,adb",
	},
}

// Checkers and Cronos keep their root filesystem in the boot ramdisk.
var (
	Checkers = Layout{
		OTAKey:   OTAKey,
		OTAValue: OTAValue,
	}
	Cronos = Layout{
		OTAKey:   OTAKey,
		OTAValue: OTAValue,
	}
	Crown = Layout{
		OTAKey:   OTAKey,
		OTAValue: OTAValue,
	}
)

// Athena's kernel mounts /vendor from its device tree with verify.
var Athena = Layout{
	Partitions: []Partition{
		{Name: "system", VerityOffset: 3229208576},
		{Name: "vendor", Mount: "vendor", VerityOffset: 297496576},
	},
	Policy:  "vendor/etc/selinux/precompiled_sepolicy",
	Patches: []Patch{AthenaFOSFlags, AthenaConnectivity, AthenaCameraServer},
	Adbd: BinPatch{
		Path:   "system/bin/adbd",
		Offset: 153016,
		Before: []byte{0x80, 0xb5, 0x00, 0xf0},
		After:  []byte{0x01, 0x20, 0x70, 0x47},
	},
	OTAKey:    OTAKey,
	OTAValue:  OTAValue,
	PropsPath: "system/etc/prop.default",
	Props: map[string]string{
		"ro.secure":              "0",
		"ro.adb.secure":          "0",
		"ro.debuggable":          "1",
		"persist.sys.usb.config": "adb",
	},

	// install-recovery.sh writes the stock recovery over TWRP on every boot.
	Gate: []string{"flash_recovery", "zygote"},
}

var Rook = Layout{
	Policy:    "sepolicy",
	PropsPath: "default.prop",
	Props: map[string]string{
		"ro.secure":              "0",
		"ro.adb.secure":          "0",
		"persist.sys.usb.config": "adb",
	},

	Gate:      []string{"surfaceflinger", "audioserver"},
	ServiceRC: "system/etc/init/hw/init.zygote32.rc",
}
