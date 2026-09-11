package update

import "testing"

// The two firmwares put the same binary on different filesystems, and both are in the field: Fire OS 5
// mounts the system partition at /system, Fire OS 6 runs it as the root filesystem. Remounting the
// wrong one fails, and the device is then stuck on whatever build it is running.
func TestMountHoldingTheBinary(t *testing.T) {
	const fireOS5 = `rootfs / rootfs ro,seclabel 0 0
tmpfs /dev tmpfs rw,seclabel,nosuid 0 0
sysfs /sys sysfs rw,seclabel,relatime 0 0
/dev/block/mmcblk0p8 /system ext4 ro,seclabel,relatime 0 0
/dev/block/mmcblk0p9 /data ext4 rw,seclabel,relatime 0 0
`

	const fireOS6 = `rootfs / rootfs rw,seclabel 0 0
/dev/root / ext4 ro,seclabel,relatime,data=ordered 0 0
tmpfs /dev tmpfs rw,seclabel,nosuid 0 0
sysfs /sys sysfs rw,seclabel,relatime 0 0
/dev/block/platform/bootdevice/by-name/userdata /data ext4 rw,seclabel 0 0
`

	for name, tc := range map[string]struct{ mounts, want string }{
		"fire os 5 mounts it at /system": {fireOS5, "/system"},
		"fire os 6 runs it as root":      {fireOS6, "/"},
		"nothing mounted says root":      {"", "/"},
	} {
		if got := mountIn(tc.mounts, "/system/app/echod/echod"); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

// /sys is a prefix of /system as a string but not as a path, and picking it would remount sysfs.
func TestMountIgnoresAPrefixThatIsNotAPathPrefix(t *testing.T) {
	const mounts = `sysfs /sys sysfs rw 0 0
/dev/root / ext4 ro 0 0
`
	if got := mountIn(mounts, "/system/app/echod/echod"); got != "/" {
		t.Errorf("%q, want /", got)
	}
}
