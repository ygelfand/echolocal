package installer

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
// ro.secure is set, so no amount of `adb root` reaches uid 0. The stage writes the boot image and
// then patches the system partition, which is where that adbd and the properties it reads live.

// Timeouts for the two reboots. Recovery comes up in a few seconds; Android takes its time.
const (
	recoveryTimeout = 90 * time.Second
	androidTimeout  = 3 * time.Minute
)

// remoteImage is where the image is staged in recovery. /tmp there is a ramdisk with room to spare.
const remoteImage = "/tmp/echolocal-boot.img"

// The order matters. What the device already is decides everything else: a device with root needs
// nothing, so it is never judged against the builds this image is known good on and the image is
// never even read. Those checks belong to the path that writes.
var flashSteps = []step{
	{"check device", checkState},
	{"check boot image", checkImage},
	{"check approval", checkApproval},
	{"reboot to recovery", bootRecovery},
	{"check target partition", checkPartition},
	{"write the boot image", writeImage},
	{"verify what was written", verifyImage},
	{"disable dm-verity", disableVerity},
	{"patch the root filesystem", patchSystem},
	{"clear the saved usb config", clearUSBConfig},
	{"reboot to android", bootAndroid},
	{"confirm root and permissive", confirmRoot},
}

// state is what the device says about itself.
type state struct {
	device     string
	build      string
	rooted     bool
	recovery   bool
	permissive bool
	enforcing  string

	// slot is ro.boot.slot_suffix, which names the half of an A/B device that is running and so the
	// one to write.
	slot string
}

// ready judges the installed system, which a device in recovery is not running: what probe reads there
// is TWRP's own root, and it answers a different question.
func (s state) ready() bool { return s.rooted && s.permissive && !s.recovery }

func (s state) String() string {
	return fmt.Sprintf("root=%t selinux=%s", s.rooted, strings.TrimSpace(s.enforcing))
}

// BootState is what a device is with respect to the boot image, for a caller deciding whether to ask
// permission before the stage starts. The stage probes again itself; this exists so the question can
// be asked before any progress display owns the terminal.
type BootState struct {
	// Ready is true when the device already has root and boots permissive, so nothing needs writing.
	Ready bool

	// Summary describes what was found, for the question.
	Summary string

	// Partition is what the question is about, which is the slot the device is running.
	Partition string
}

// Probe reports what a device is without changing anything.
func Probe(d *device.Device) (BootState, error) {
	s, err := probe(d)
	return BootState{Ready: s.ready(), Summary: s.String(), Partition: bootimg.Partition(s.slot)}, err
}

// probe reads the device's state. It runs before the flash and again after, so the two can never
// disagree about what counts as done.
func probe(d *device.Device) (state, error) {
	var s state
	var err error

	if s.recovery, err = d.InRecovery(); err != nil {
		return s, err
	}
	if s.device, err = d.Getprop("ro.product.device"); err != nil {
		return s, err
	}
	if s.slot, err = d.Getprop("ro.boot.slot_suffix"); err != nil {
		return s, err
	}
	if s.build, err = d.Getprop("ro.build.version.incremental"); err != nil {
		return s, err
	}
	if s.rooted, err = d.IsRoot(); err != nil {
		return s, err
	}

	s.enforcing, _ = d.Shell("getenforce")
	s.permissive = !strings.EqualFold(strings.TrimSpace(s.enforcing), "enforcing")
	return s, nil
}

// checkState reads what the device is and decides whether anything needs writing. It never refuses a
// device on its build: that only matters when an image is about to be written, and a device that is
// already root may be running something else entirely that works.
func checkState(r *run) (string, bool, error) {
	s, err := probe(r.d)
	if err != nil {
		return "", false, err
	}
	r.state = s

	return fmt.Sprintf("%s, build %s, %s", s.device, s.build, s), false, nil
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
	detail := fmt.Sprintf("%s, %d bytes, %s", r.cfg.BootImageFrom, bootimg.Ours.Size, bootimg.Ours.SHA256[:12])

	// getprop in recovery describes the recovery image, which is somebody else's build: it names neither
	// the hardware nor the system this ramdisk pairs with, so there is nothing to compare against.
	if r.state.recovery {
		return detail, false, nil
	}

	untested, err := bootimg.Ours.Supports(r.state.device, r.state.build)
	if err != nil {
		return "", false, err
	}
	if untested != "" {
		detail += "; " + untested
	}
	return detail, false, nil
}

// checkApproval is the last gate before anything is written.
func checkApproval(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}
	if !r.cfg.Approved {
		return "", false, errors.New("overwriting the boot partition was not approved")
	}
	return "approved", false, nil
}

// done reports whether the writing steps have anything to do. What is tested is root, not which image
// is installed: a device that has it needs nothing from us whatever it is running.
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

// clearUSBConfig removes the persisted persist.sys.usb.config, which is where mtp gets saved. A device
// that boots with that value brings up no adb at all.
//
// It runs in both stages and is not gated on what the device already is: whatever writes the value can
// still be running, so removing it once is no guarantee it is gone.
func clearUSBConfig(r *run) (string, bool, error) {
	const path = "/data/property/persist.sys.usb.config"

	there, err := r.d.Exists(path)
	if err != nil {
		return "", false, err
	}
	if !there {
		return "none saved", true, nil
	}
	if _, err := r.d.Shell("rm -f " + path); err != nil {
		return "", false, err
	}
	return "removed", false, nil
}

func bootRecovery(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}

	if !r.state.recovery {
		if err := r.d.Reboot(device.StateRecovery); err != nil {
			return "", false, err
		}
		ctx, cancel := context.WithTimeout(r.ctx, recoveryTimeout)
		defer cancel()

		if err := r.d.Wait(ctx, device.StateRecovery); err != nil {
			return "", false, err
		}
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

// checkPartition resolves the target and refuses anything that is not the size a boot slot measures.
// Writing a boot image into a partition of some other size is how a device stops booting.
func checkPartition(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}

	target, err := r.partition()
	if err != nil {
		return "", false, err
	}

	node, err := r.d.Shell("readlink -f " + bootimg.Node(r.state.slot))
	if err != nil {
		return "", false, err
	}
	node = strings.TrimSpace(node)

	raw, err := r.d.Shell("blockdev --getsize64 " + bootimg.Node(r.state.slot))
	if err != nil {
		return "", false, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return "", false, fmt.Errorf("%s resolves to %s, whose size reads as %q: refusing to write",
			target, node, strings.TrimSpace(raw))
	}
	if !bootimg.KnownPartition(size) {
		return "", false, fmt.Errorf("%s resolves to %s of %d bytes, want one of %v: refusing to write",
			target, node, size, bootimg.PartitionSizes)
	}
	return fmt.Sprintf("%s → %s, %d bytes", target, node, size), false, nil
}

// partition is the slot to write, refusing a device that names none: "boot" on its own is not a
// partition here, and writing it would resolve to nothing or to something else entirely.
func (r *run) partition() (string, error) {
	if r.state.slot == "" {
		return "", errUnknownSlot
	}
	return bootimg.Partition(r.state.slot), nil
}

var errUnknownSlot = errors.New("device names no boot slot in ro.boot.slot_suffix")

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
	if _, err := r.d.Shell(fmt.Sprintf("dd if=%s of=%s bs=1048576 && sync", remoteImage, bootimg.Node(r.state.slot))); err != nil {
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
		bootimg.Node(r.state.slot), block, bootimg.Ours.Size/block))
	if err != nil {
		return "", false, err
	}
	if got != bootimg.Ours.SHA256 {
		return "", false, fmt.Errorf("the partition hashes to %s, want %s: left in recovery, re-run to write it again",
			got, bootimg.Ours.SHA256)
	}
	return got[:12] + " matches", false, nil
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

// confirmRoot is the only judge of whether the flash worked. It asks the same question done() does, so
// a stage that reports success is one a re-run would skip.
func confirmRoot(r *run) (string, bool, error) {
	s, err := probe(r.d)
	if err != nil {
		return "", false, err
	}
	if !s.ready() {
		return "", false, fmt.Errorf("still %s", s)
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
