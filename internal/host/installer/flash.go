package installer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ygelfand/echolocal/internal/host/bootimg"
	"github.com/ygelfand/echolocal/internal/host/device"
)

// The flash stage puts echod's boot image on the device, and is a no-op once it is there. It is
// separate from installing echod because it reboots twice and because it is the only step that writes
// outside /system — worth being able to run on its own, as often as needed, before anything else is
// attempted.
//
// Root is the reason it exists. A stock biscuit runs Amazon's adbd, which drops privileges however
// ro.secure is set, so no amount of `adb root` reaches uid 0. Our image carries an adbd that honours
// it, and a kernel cmdline with androidboot.selinux=permissive, without which echod cannot open its
// listening socket at all.

// Timeouts for the two reboots. Recovery comes up in a few seconds; Android takes its time.
const (
	recoveryTimeout = 90 * time.Second
	androidTimeout  = 3 * time.Minute
)

// remoteImage is where the image is staged in recovery. /tmp there is a ramdisk with room to spare.
const remoteImage = "/tmp/echolocal-boot.img"

// The order matters. What the device already is decides everything else: a device with root and a
// permissive kernel needs nothing, so it is never judged against the builds this image is known good
// on and the image is never even read. Those checks belong to the path that writes.
var flashSteps = []step{
	{"check device", checkState},
	{"check boot image", checkImage},
	{"check approval", checkApproval},
	{"reboot to recovery", bootRecovery},
	{"check target partition", checkPartition},
	{"write the boot image", writeImage},
	{"verify what was written", verifyImage},
	{"unmount recovery userdata", unmountRecoveryUserdata},
	{"reboot to android", bootAndroid},
	{"confirm root and policy", confirmPolicy},
}

// state is what the device says about itself. ours is deliberately not a hash: a device running our
// image has both permissive and a root adbd, and no other image on this hardware gives both.
type state struct {
	device     string
	build      string
	rooted     bool
	permissive bool
	enforcing  string
}

func (s state) ready() bool { return s.rooted && s.permissive }

func (s state) String() string {
	return fmt.Sprintf("root=%t selinux=%s enforce=%q", s.rooted, policy(s.permissive), s.enforcing)
}

func policy(permissive bool) string {
	if permissive {
		return "permissive"
	}
	return "enforcing"
}

// BootState is what a device is with respect to the boot image, for a caller deciding whether to ask
// permission before the stage starts. The stage probes again itself; this exists so the question can
// be asked before any progress display owns the terminal.
type BootState struct {
	// Ready is true when the device already has root and a permissive kernel, so nothing needs
	// writing.
	Ready bool

	// Summary describes what was found, for the question.
	Summary string
}

// Probe reports what a device is without changing anything.
func Probe(d *device.Device) (BootState, error) {
	s, err := probe(d)
	return BootState{Ready: s.ready(), Summary: s.String()}, err
}

// probe reads the device's state. It runs before the flash and again after, so the two can never
// disagree about what counts as done.
func probe(d *device.Device) (state, error) {
	var s state
	var err error

	if s.device, err = d.Getprop("ro.product.device"); err != nil {
		return s, err
	}
	if s.build, err = d.Getprop("ro.build.version.incremental"); err != nil {
		return s, err
	}
	if s.rooted, err = d.IsRoot(); err != nil {
		return s, err
	}

	asked, err := d.Getprop("ro.boot.selinux")
	if err != nil {
		return s, err
	}
	s.enforcing, _ = d.Shell("getenforce")
	s.enforcing = strings.TrimSpace(s.enforcing)
	s.permissive = permissiveFrom(asked, s.enforcing)

	return s, nil
}

// permissiveFrom judges the two answers together: ro.boot.selinux is what the booted image asked for,
// getenforce is what is in force. A permissive cmdline on an enforcing kernel would leave echod unable
// to open its socket and looking like a bug in echod.
func permissiveFrom(asked, enforcing string) bool {
	return asked == "permissive" && !strings.EqualFold(enforcing, "enforcing")
}

// checkState reads what the device is and decides whether anything needs writing. It never refuses a
// device on its build: that only matters when an image is about to be written, and a device that is
// already root and permissive may be running something else entirely that works.
func checkState(r *run) (string, bool, error) {
	s, err := probe(r.d)
	if err != nil {
		return "", false, err
	}
	r.state = s

	return fmt.Sprintf("%s, Fire OS %s, %s", s.device, bootimg.Firmware(s.build), s), false, nil
}

// checkImage verifies the file about to be written and that it belongs on this device. Both together,
// because the pairing is what matters: the right image on the wrong build is a device that boots
// someone else's ramdisk against this system partition.
func checkImage(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}
	if len(r.cfg.BootImage) == 0 {
		return "", false, errors.New("no boot image given")
	}
	if err := bootimg.Ours.Verify(r.cfg.BootImageFrom, r.cfg.BootImage); err != nil {
		return "", false, err
	}
	if err := bootimg.Ours.Supports(r.state.device, r.state.build); err != nil {
		return "", false, err
	}
	return fmt.Sprintf("%s, %d bytes, %s", r.cfg.BootImageFrom, bootimg.Ours.Size, bootimg.Ours.SHA256[:12]), false, nil
}

// checkApproval is the last gate before anything is written.
func checkApproval(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}
	if !r.cfg.Approved {
		return "", false, fmt.Errorf("overwriting %s was not approved", bootimg.Partition)
	}
	return "approved", false, nil
}

// done reports whether the writing steps have anything to do. What is tested is root and permissive,
// not which image is installed: a device that has both needs nothing from us whatever it is running.
func (r *run) done() (string, bool) {
	if r.state.ready() {
		return "already root and permissive", true
	}
	return "", false
}

// settle waits for recovery to be answering again. Wait returns as soon as it is, so this costs
// nothing when adbd never went away.
func (r *run) settle() error {
	ctx, cancel := context.WithTimeout(r.ctx, recoveryTimeout)
	defer cancel()

	return r.d.Wait(ctx, device.StateRecovery)
}

// stageTries is how many times the image is sent before giving up. One restart of recovery's adbd is
// what this is for, and it only happens once per boot.
const stageTries = 3

// stage puts the image on the device and does not return until what is there is the image.
func (r *run) stage() error {
	tries := make([]string, 0, stageTries)

	for try := range stageTries {
		if err := r.settle(); err != nil {
			return err
		}
		if err := r.d.WriteFile(remoteImage, r.cfg.BootImage, 0o644); err != nil && try == stageTries-1 {
			return err
		}
		if err := r.settle(); err != nil {
			return err
		}

		staged, err := sha256Of(r.d, "cat "+remoteImage)
		if err != nil && try == stageTries-1 {
			return err
		}
		if staged == bootimg.Ours.SHA256 {
			return nil
		}
		tries = append(tries, fmt.Sprintf("%s bytes %s", sizeOf(r.d, remoteImage), staged))
	}

	return fmt.Errorf("the staged image never matched. tries: %s. want: %d bytes %s",
		strings.Join(tries, "; "), bootimg.Ours.Size, bootimg.Ours.SHA256)
}

// sizeOf is what the device says is there, which is what tells a short write from a wrong one.
func sizeOf(d *device.Device, remote string) string {
	out, err := d.Shell("wc -c < " + remote)
	if err != nil {
		return "?"
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "?"
	}
	return fields[0]
}

func bootRecovery(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}

	if err := r.d.Reboot(device.StateRecovery); err != nil {
		return "", false, err
	}
	ctx, cancel := context.WithTimeout(r.ctx, recoveryTimeout)
	defer cancel()

	if err := r.d.Wait(ctx, device.StateRecovery); err != nil {
		return "", false, err
	}

	// A root adbd is what distinguishes a usable recovery from the stock one, and it cannot be known
	// from Android beforehand: nothing readable there says what is in the recovery partition. So this
	// is where a device without TWRP finds out, and it goes back to Android rather than being left
	// sitting in recovery.
	root, err := r.d.IsRoot()
	if err != nil {
		return "", false, err
	}
	if !root {
		if err := r.d.Reboot(""); err != nil {
			return "", false, fmt.Errorf("recovery is not running adbd as root, and rebooting back failed: %w", err)
		}
		return "", false, errors.New("recovery is not running adbd as root: this needs TWRP installed as the recovery partition; rebooting back to Android")
	}
	return "root recovery", false, nil
}

// checkPartition resolves the target and refuses anything that is not the 16 MB boot partition.
//
// The name matters: to the amonet bootloader boot_a is a 110 MB partition while to the kernel it is
// the 16 MB one, so only the _x form is unambiguous — and the size is checked in case a name ever
// resolves somewhere else. Writing a 7 MB boot image into a 110 MB partition is how a device stops
// booting.
func checkPartition(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}

	node, err := r.d.Shell("readlink -f " + bootimg.Node)
	if err != nil {
		return "", false, err
	}
	node = strings.TrimSpace(node)

	raw, err := r.d.Shell("blockdev --getsize64 " + bootimg.Node)
	if err != nil {
		return "", false, err
	}
	size := strings.TrimSpace(raw)
	if size != fmt.Sprint(bootimg.PartitionSize) {
		return "", false, fmt.Errorf("%s resolves to %s of %s bytes, want %d: refusing to write",
			bootimg.Partition, node, size, bootimg.PartitionSize)
	}
	return fmt.Sprintf("%s → %s, %s bytes", bootimg.Partition, node, size), false, nil
}

func writeImage(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}

	// Recovery restarts its adbd shortly after it comes up, which cuts off whatever is running: the
	// stream ends early and dd, reading a pipe, writes what arrived and exits happily. So the transfer
	// is staged, proved, and tried again if it was cut. Nothing is at risk while this repeats — the
	// image is on a ramdisk and the partition is untouched until the hash matches.
	if err := r.stage(); err != nil {
		return "", false, err
	}

	// sync rather than conv=fsync: busybox builds differ on whether they accept it, and a dd that
	// rejects the flag would fail after the partition was already open for writing.
	if _, err := r.d.Shell(fmt.Sprintf("dd if=%s of=%s bs=1M && sync", remoteImage, bootimg.Node)); err != nil {
		return "", false, err
	}
	return "written", false, nil
}

// verifyImage reads the partition back. This is the reason writing from recovery is defensible: the
// bytes that will boot are hashed, rather than assumed to have landed.
func verifyImage(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}

	if err := r.settle(); err != nil {
		return "", false, err
	}

	// Small blocks and a count, not one read of the whole image: a short read from a single huge
	// request would hash fewer bytes and report a mismatch on a write that was fine.
	const block = 512
	if bootimg.Ours.Size%block != 0 {
		return "", false, fmt.Errorf("image size %d is not a multiple of %d", bootimg.Ours.Size, block)
	}
	got, err := sha256Of(r.d, fmt.Sprintf("dd if=%s bs=%d count=%d 2>/dev/null",
		bootimg.Node, block, bootimg.Ours.Size/block))
	if err != nil {
		return "", false, err
	}
	if got != bootimg.Ours.SHA256 {
		return "", false, fmt.Errorf("the partition hashes to %s, want %s: left in recovery, re-run to write it again",
			got, bootimg.Ours.SHA256)
	}
	return got[:12] + " matches", false, nil
}

const userdataNode = "/dev/block/platform/mtk-msdc.0/by-name/userdata"

// unmountRecoveryUserdata leaves TWRP's automatic userdata mounts in a clean state before Android
// boots. Some TWRP builds mount the same ext4 filesystem at both /data and /sdcard; rebooting while
// those mounts are live can abort its journal and make Android remount /data read-only.
func unmountRecoveryUserdata(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}

	node, err := r.d.Shell("readlink -f " + userdataNode)
	if err != nil {
		return "", false, fmt.Errorf("resolving userdata: %w", err)
	}
	node = strings.TrimSpace(node)
	if node == "" || !strings.HasPrefix(node, "/dev/block/") {
		return "", false, fmt.Errorf("userdata resolved to unsafe block device %q", node)
	}

	mounts, err := recoveryUserdataMounts(r.d, node)
	if err != nil {
		return "", false, err
	}
	if len(mounts) == 0 {
		return "already unmounted", false, nil
	}

	if _, err := r.d.Shell("sync"); err != nil {
		return "", false, fmt.Errorf("syncing recovery filesystems: %w", err)
	}
	// /sdcard is the second mount on the affected TWRP image, so release it before /data.
	for _, target := range []string{"/sdcard", "/data"} {
		if !contains(mounts, target) {
			continue
		}
		if _, err := r.d.Shell("umount " + target); err != nil {
			return "", false, fmt.Errorf("unmounting userdata from %s: %w", target, err)
		}
	}
	if _, err := r.d.Shell("sync"); err != nil {
		return "", false, fmt.Errorf("syncing after unmount: %w", err)
	}

	remaining, err := recoveryUserdataMounts(r.d, node)
	if err != nil {
		return "", false, err
	}
	if len(remaining) != 0 {
		return "", false, fmt.Errorf("userdata is still mounted at %s; refusing to reboot",
			strings.Join(remaining, ", "))
	}
	return strings.Join(mounts, " and "), false, nil
}

func recoveryUserdataMounts(d *device.Device, node string) ([]string, error) {
	raw, err := d.Shell("cat /proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("reading recovery mounts: %w", err)
	}
	return parseUserdataMounts(raw, node)
}

func parseUserdataMounts(raw, node string) ([]string, error) {
	found := map[string]bool{}
	for line := range strings.SplitSeq(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || (fields[0] != node && fields[0] != userdataNode) {
			continue
		}
		switch fields[1] {
		case "/data", "/sdcard":
			found[fields[1]] = true
		default:
			return nil, fmt.Errorf("userdata is unexpectedly mounted at %s; refusing to reboot", fields[1])
		}
	}

	// Keep the unmount order deterministic and safe for TWRP's duplicate mount.
	var mounts []string
	for _, target := range []string{"/sdcard", "/data"} {
		if found[target] {
			mounts = append(mounts, target)
		}
	}
	return mounts, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func bootAndroid(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}
	if err := r.d.Reboot(""); err != nil {
		return "", false, err
	}

	ctx, cancel := context.WithTimeout(r.ctx, androidTimeout)
	defer cancel()
	if err := r.d.WaitBooted(ctx); err != nil {
		return "", false, err
	}
	return "booted", false, nil
}

// confirmPolicy is the same probe as before, and the only judge of whether this worked.
func confirmPolicy(r *run) (string, bool, error) {
	s, err := probe(r.d)
	if err != nil {
		return "", false, err
	}

	switch {
	case !s.rooted && !s.permissive:
		return "", false, fmt.Errorf("still %s: the image did not take", s)
	case !s.rooted:
		return "", false, fmt.Errorf("permissive but adbd is not root (%s)", s)
	case !s.permissive:
		return "", false, fmt.Errorf("root but %s: echod cannot open its socket", s)
	}
	return s.String(), false, nil
}

// sha256Of hashes what a command writes, on the device. Recovery has sha256sum; hashing on the host
// would mean pulling 7 MB twice for no gain.
func sha256Of(d *device.Device, cmd string) (string, error) {
	out, err := d.Shell(cmd + " | sha256sum")
	if err != nil {
		return "", err
	}
	sum, _, _ := strings.Cut(strings.TrimSpace(out), " ")
	if len(sum) != 64 {
		return "", fmt.Errorf("unreadable sha256 in %q", strings.TrimSpace(out))
	}
	return sum, nil
}
