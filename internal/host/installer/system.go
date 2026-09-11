package installer

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ygelfand/echolocal/internal/android/services"
	"github.com/ygelfand/echolocal/internal/host/assets"
	"github.com/ygelfand/echolocal/internal/host/sepolicy"
	"github.com/ygelfand/echolocal/internal/host/sysimg"
	"github.com/ygelfand/echolocal/internal/layout"
)

// rcEdit rewrites one rc file and names what it changed.
type rcEdit func(string) (string, []string)

// patchInitRC applies the edits to every init rc file, reading each once and writing it back only if
// one of them changed it.
func (r *run) patchInitRC(at string, edits ...rcEdit) (int, error) {
	found, err := r.initRC(at)
	if err != nil {
		return 0, err
	}

	var count int
	for _, path := range found {
		rc, err := r.d.ReadFile(path)
		if err != nil {
			return count, err
		}

		body, changed := string(rc), 0
		for _, edit := range edits {
			out, did := edit(body)
			body, changed = out, changed+len(did)
		}

		if changed == 0 {
			continue
		}
		if err := r.writeInPlace(path, []byte(body)); err != nil {
			return count, err
		}
		count += changed
	}
	return count, nil
}

// initRC lists every rc file init reads.
func (r *run) initRC(at string) ([]string, error) {
	found, err := r.d.Shell(fmt.Sprintf("ls %s/*.rc %s/system/etc/init/*.rc %s/vendor/etc/init/*.rc 2>/dev/null",
		at, at, at))
	if err != nil {
		return nil, fmt.Errorf("listing the init rc files: %w", err)
	}
	return strings.Fields(found), nil
}

// A user build's init forces SELinux enforcing whatever the kernel command line asks for: the check is
// compiled out, so there is nothing to set. What is left is to let init undo it, which is these two
// changes together. Neither works alone.
const (
	// enforceOff is an init command rather than a service, so it runs in init's own domain, in the
	// first trigger of the boot, before anything init would otherwise have to start under enforcement.
	enforceOff = "write /sys/fs/selinux/enforce 0"

	// initDomain is refused that write by the policy Amazon ships. Marked permissive it is logged and
	// allowed, which costs one bit and adds no rule.
	initDomain = "init"

	policyPath = "/sepolicy"
)

// permissiveAtBoot leaves the device permissive from the moment init loads its policy, which is before
// it starts anything. Both halves belong to the flash stage: root and permissive are then two things
// one boot image plus one patched system give together, and either missing means the same repair.
func (r *run) permissiveAtBoot(at string) (string, error) {
	policy, err := r.d.ReadFile(at + policyPath)
	if err != nil {
		return "", err
	}

	patched, err := sepolicy.Permissive(policy, initDomain)
	if err != nil {
		return "", fmt.Errorf("marking %s permissive: %w", initDomain, err)
	}
	if !bytes.Equal(policy, patched) {
		if err := r.writeInPlace(at+policyPath, patched); err != nil {
			return "", err
		}
	}

	rc, err := r.enforceOff(at)
	if err != nil {
		return "", err
	}
	return "sepolicy, " + rc, nil
}

// enforceOff adds the command that takes the device permissive, and says where it ended up. It looks
// through every rc file rather than only the one it writes, so a device that has it somewhere else does
// not collect a second copy.
func (r *run) enforceOff(at string) (string, error) {
	found, err := r.initRC(at)
	if err != nil {
		return "", err
	}

	for _, path := range found {
		rc, err := r.d.ReadFile(path)
		if err != nil {
			return "", err
		}
		if bytes.Contains(rc, []byte(enforceOff)) {
			return strings.TrimPrefix(path, at), nil
		}
	}

	path := at + "/init.rc"
	rc, err := r.d.ReadFile(path)
	if err != nil {
		return "", err
	}

	body := strings.TrimRight(string(rc), "\n") + "\n\non early-init\n    " + enforceOff + "\n"
	if err := r.writeInPlace(path, []byte(body)); err != nil {
		return "", err
	}
	return "/init.rc", nil
}

// The boot image is only half of it. This is a system-as-root device, so the root filesystem — and
// with it default.prop and the fstab — lives on the system partition, and echod needs both changed.
// Mounting that read-write means dm-verity has to go first, in the metadata and in the fstab both.

// disableVerity overwrites the verity metadata's magic, which is what a kernel checks before it will
// mount the partition it describes.
//
// It refuses anything that is not one of the two magics it knows. The offset is a property of the
// image this was measured on, so a different one would have us writing four bytes into the middle of
// somebody's filesystem.
func disableVerity(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}

	dev, err := r.systemNode()
	if err != nil {
		return "", false, err
	}

	magic, err := r.peek(dev, sysimg.VerityOffset, len(sysimg.DisabledBytes))
	if err != nil {
		return "", false, err
	}

	off, err := sysimg.Verity(magic)
	if err != nil {
		return "", false, err
	}
	if off {
		return "already disabled", true, nil
	}

	if err := r.poke(dev, sysimg.VerityOffset, sysimg.DisabledBytes); err != nil {
		return "", false, err
	}

	if magic, err = r.peek(dev, sysimg.VerityOffset, len(sysimg.DisabledBytes)); err != nil {
		return "", false, err
	}
	if off, err := sysimg.Verity(magic); err != nil || !off {
		return "", false, fmt.Errorf("verity metadata reads %x after the write", magic)
	}
	return "disabled", false, nil
}

// patchSystem writes what echod needs onto the system partition, and puts it back read-only however
// it ends.
func patchSystem(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}

	at, err := r.mountSystem()
	if err != nil {
		return "", false, err
	}
	defer func() {
		if _, err := r.d.Shell("sync; umount " + at); err != nil {
			slog.Error("unmounting the system partition failed", "at", at, "err", err)
		}
	}()

	files := []struct {
		patch sysimg.Patch
		data  []byte
	}{
		{sysimg.DefaultProp, assets.DefaultProp()},
		{sysimg.Fstab, assets.Fstab()},
	}

	written := make([]string, 0, len(files)+3)
	for _, f := range files {
		if err := f.patch.Verify(f.data); err != nil {
			return "", false, err
		}
		if err := r.writeInPlace(at+"/"+f.patch.Path, f.data); err != nil {
			return "", false, err
		}
		written = append(written, f.patch.Path)
	}

	// init reads a service block once, at boot. The install has to start echod in the run that writes
	// it, so this one edit cannot wait for the reboot the rest of the rc changes do.
	if _, err := r.patchInitRC(at,
		func(s string) (string, []string) { return services.AsRoot(s, layout.ServiceName) },
	); err != nil {
		return "", false, err
	}
	written = append(written, layout.ServiceName)

	permissive, err := r.permissiveAtBoot(at)
	if err != nil {
		return "", false, err
	}
	written = append(written, permissive)

	adbd, err := r.patchAdbd(at)
	if err != nil {
		return "", false, err
	}
	return strings.Join(append(written, "adbd "+adbd), ", "), false, nil
}

// deAmazon stops Amazon's services and keeps them from starting again. It is an install step rather
// than a flash one: root says nothing about whether it has been done, so a device that already has
// root would otherwise never get it.
//
// The rc files live on the system partition, which the install has already remounted, and paths here
// are absolute because this runs against a booted Android rather than a mount in recovery.
func deAmazon(r *run) (string, bool, error) {
	disable, enable := services.DisabledSet(), services.EnabledSet()

	changed, err := r.patchInitRC("",
		func(s string) (string, []string) { return services.Disable(s, disable) },
		func(s string) (string, []string) { return services.Enable(s, enable) },
	)
	if err != nil {
		return "", false, err
	}

	stopped, err := r.stopServices()
	if err != nil {
		return "", false, err
	}

	if err := r.neuterOTA(""); err != nil {
		return "", false, err
	}

	if changed == 0 && len(stopped) == 0 {
		return fmt.Sprintf("%d already disabled and none running", len(services.Disabled)), true, nil
	}

	// The rc edits are what the next boot reads. Stopping frees the hardware now, but init started
	// these once already and a boot without them is the state this is after.
	r.reboot = true
	return fmt.Sprintf("%d rc entries changed, %d stopped", changed, len(stopped)), false, nil
}

// writeInPlace replaces a file's contents without unlinking it, which keeps its inode, mode, owner and
// label, and leaves the original untouched when the write is refused.
//
// adb push cannot be used for this. It unlinks the destination before creating it, so a push to
// somewhere it may not create destroys the file it was replacing.
func (r *run) writeInPlace(path string, data []byte) error {
	const staged = "/data/local/tmp/echolocal-staged"

	if err := r.d.WriteFile(staged, data, 0o600); err != nil {
		return err
	}
	defer func() { _, _ = r.d.Shell("rm -f " + staged) }()

	_, err := r.d.Shell(fmt.Sprintf("cat %s > %s", staged, path))
	return err
}

// stopServices stops the ones running now, which is what frees the audio devices before echod is
// asked to take them. Disabling only decides what the next boot starts.
func (r *run) stopServices() ([]string, error) {
	// One read of every service init knows about, rather than a getprop per name.
	props, err := r.d.Shell("getprop")
	if err != nil {
		return nil, err
	}
	running := runningServices(props)

	var stopped []string
	for _, name := range services.Disabled {
		if !running[name] {
			continue
		}
		if err := r.d.Setprop("ctl.stop", name); err != nil {
			return stopped, err
		}
		stopped = append(stopped, name)
	}
	return stopped, nil
}

// runningServices reads `getprop` output, whose lines look like [init.svc.mixer]: [running].
func runningServices(props string) map[string]bool {
	out := map[string]bool{}
	for line := range strings.SplitSeq(props, "\n") {
		name, state, ok := strings.Cut(line, "]: [")
		if !ok || !strings.HasPrefix(name, "[init.svc.") {
			continue
		}
		if strings.TrimSuffix(state, "]") == "running" {
			out[strings.TrimPrefix(name, "[init.svc.")] = true
		}
	}
	return out
}

// patchAdbd flips the four bytes that stop the stock adbd dropping privileges. A build these offsets
// were not taken from is refused rather than overwritten.
func (r *run) patchAdbd(dir string) (string, error) {
	path := dir + "/" + sysimg.Adbd.Path

	current, err := r.peek(path, sysimg.Adbd.Offset, len(sysimg.Adbd.Before))
	if err != nil {
		return "", err
	}

	switch {
	case bytes.Equal(current, sysimg.Adbd.After):
		return "already patched", nil
	case !bytes.Equal(current, sysimg.Adbd.Before):
		return "", fmt.Errorf("%s reads %x at %d, which is neither the stock bytes nor ours",
			path, current, sysimg.Adbd.Offset)
	}

	if err := r.poke(path, sysimg.Adbd.Offset, sysimg.Adbd.After); err != nil {
		return "", err
	}
	return "patched", nil
}

// peek reads n bytes at a byte offset, of a file or a block device.
func (r *run) peek(path string, at int64, n int) ([]byte, error) {
	raw, err := r.d.Shell(fmt.Sprintf("toybox dd if=%s bs=1 skip=%d count=%d 2>/dev/null | od -An -tx1 | tr -d ' \\n'",
		path, at, n))
	if err != nil {
		return nil, err
	}

	got, err := hex.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("reading %s at %d: %q", path, at, raw)
	}
	if len(got) != n {
		return nil, fmt.Errorf("read %d bytes of %s at %d, want %d", len(got), path, at, n)
	}
	return got, nil
}

// poke writes bytes at a byte offset, leaving the rest of the file alone. The dd on PATH in recovery is
// built without conv, and without notrunc this would cut a file off at the write.
func (r *run) poke(path string, at int64, b []byte) error {
	var escaped strings.Builder
	for _, c := range b {
		fmt.Fprintf(&escaped, `\%03o`, c)
	}

	_, err := r.d.Shell(fmt.Sprintf("printf '%s' | toybox dd of=%s bs=1 seek=%d conv=notrunc 2>/dev/null",
		escaped.String(), path, at))
	return err
}

// systemMount is recovery's own mountpoint for the running slot's system partition. Its fstab names
// the device and the filesystem and mounts it read-write, so none of that is repeated here.
const systemMount = "/system_root"

// mountSystem gets the system partition mounted and says where. Mounting it twice is refused as busy,
// so it is unmounted first and the state is the same whether or not anything had it.
func (r *run) mountSystem() (string, error) {
	_, _ = r.d.Shell("umount " + systemMount + " 2>/dev/null")

	if _, err := r.d.Shell("mount " + systemMount); err != nil {
		return "", fmt.Errorf("mounting %s: %w", systemMount, err)
	}
	return systemMount, nil
}

// neuterOTA stops Fire OS updating over this install. An update writes the other slot and switches to
// it, which replaces the system these files were written to and leaves a device we cannot reach.
func (r *run) neuterOTA(at string) error {
	path := at + "/system/build.prop"

	current, err := r.d.ReadFile(path)
	if err != nil {
		return err
	}
	return r.writeInPlace(path, setProp(current, sysimg.OTAKey, sysimg.OTAValue))
}

// setProp replaces a property in a build.prop, or appends it where there is none.
func setProp(file []byte, key, value string) []byte {
	line := key + "=" + value

	lines := strings.Split(string(file), "\n")
	for i, at := range lines {
		if strings.HasPrefix(at, key+"=") {
			lines[i] = line
			return []byte(strings.Join(lines, "\n"))
		}
	}
	return []byte(strings.TrimRight(string(file), "\n") + "\n" + line + "\n")
}

// systemNode is the system partition for the slot the device is running.
func (r *run) systemNode() (string, error) {
	if r.state.slot == "" {
		return "", errUnknownSlot
	}
	return sysimg.Node(r.state.slot), nil
}
