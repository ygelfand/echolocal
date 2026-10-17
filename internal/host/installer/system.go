package installer

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/ygelfand/echolocal/internal/android/services"
	"github.com/ygelfand/echolocal/internal/host/assets"
	"github.com/ygelfand/echolocal/internal/host/sepolicy"
	"github.com/ygelfand/echolocal/internal/host/sysimg"
)

type rcEdit func(string) (string, []string)

func (r *run) patchInitRC(at string, edits ...rcEdit) (int, error) {
	found, err := r.initRC(at)
	if err != nil {
		return 0, err
	}

	var count int
	for i, path := range found {
		if i > 0 {
			time.Sleep(250 * time.Millisecond)
		}
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

func (r *run) initRC(at string) ([]string, error) {
	link, code, err := r.d.ShellCode("readlink " + at + "/vendor")
	if err != nil {
		return nil, fmt.Errorf("reading the vendor link: %w", err)
	}
	if code != 0 {
		link = ""
	}

	globs := []string{
		at + "/system/etc/init/*.rc",
		vendorUnder(at, strings.TrimSpace(link)) + "/etc/init/*.rc",
	}
	if r.board().SystemAsRoot {
		globs = append([]string{at + "/*.rc"}, globs...)
	}

	var found []string
	for _, glob := range globs {
		out, code, err := r.d.ShellCode("ls " + glob + " 2>/dev/null")
		if err != nil {
			return nil, fmt.Errorf("listing the init rc files: %w", err)
		}
		if code == 0 {
			found = append(found, strings.Fields(out)...)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no init rc files under %q", at+"/")
	}
	return found, nil
}

// An absolute vendor link names a path inside the image, not inside recovery.
func vendorUnder(at, link string) string {
	switch {
	case link == "":
		return at + "/vendor"
	case strings.HasPrefix(link, "/"):
		return at + link
	}
	return at + "/" + link
}

// A user build's init forces SELinux enforcing whatever the kernel command line asks.
const (
	enforceOff = "write /sys/fs/selinux/enforce 0"

	// Amazon's shipped policy refuses init that write.
	initDomain = "init"

	policyPath = "/sepolicy"
)

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

// This is a system-as-root device: default.prop and the fstab live on the system partition.

// The kernel checks the verity metadata's magic before it mounts the partition.
func disableVerity(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}
	if r.system().VerityOffset == 0 {
		return "no verity metadata on this board", true, nil
	}

	dev, err := r.systemNode()
	if err != nil {
		return "", false, err
	}
	at := r.system().VerityOffset

	magic, err := r.peek(dev, at, len(sysimg.DisabledBytes))
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

	if err := r.poke(dev, at, sysimg.DisabledBytes); err != nil {
		return "", false, err
	}

	if magic, err = r.peek(dev, at, len(sysimg.DisabledBytes)); err != nil {
		return "", false, err
	}
	if off, err := sysimg.Verity(magic); err != nil || !off {
		return "", false, fmt.Errorf("verity metadata reads %x after the write", magic)
	}
	return "disabled", false, nil
}

func patchSystem(r *run) (string, bool, error) {
	if detail, skip := r.done(); skip {
		return detail, true, nil
	}
	if r.system().Empty() {
		return "the root filesystem is in the boot image", true, nil
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

	layout := r.system()

	written := make([]string, 0, len(layout.Patches)+3)
	for _, p := range layout.Patches {
		data, err := assets.Patch(p.File)
		if err != nil {
			return "", false, err
		}
		if err := p.Verify(data); err != nil {
			return "", false, err
		}
		if err := r.writeInPlace(at+"/"+p.Path, data); err != nil {
			return "", false, err
		}
		written = append(written, p.Path)
	}

	if did, err := r.setProps(at, layout); err != nil {
		return "", false, err
	} else if did != "" {
		written = append(written, did)
	}

	for _, gated := range layout.Gate {
		if _, err := r.patchInitRC(at,
			func(s string) (string, []string) { return services.Gated(s, gated) },
		); err != nil {
			return "", false, err
		}
		written = append(written, gated+" gated")
	}

	if did, err := r.takeService(at, layout); err != nil {
		return "", false, err
	} else if did != "" {
		written = append(written, did)
	}

	// init reads a service block once, at boot.
	name := r.board().ServiceName
	if _, err := r.patchInitRC(at,
		func(s string) (string, []string) { return services.AsRoot(s, name) },
	); err != nil {
		return "", false, err
	}
	written = append(written, name)

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

func serviceAsRoot(r *run) (string, bool, error) {
	if r.ownService() {
		return "echod's own service runs as root", true, nil
	}
	name := r.board().ServiceName

	changed, err := r.patchInitRC("",
		func(s string) (string, []string) { return services.AsRoot(s, name) },
	)
	if err != nil {
		return "", false, err
	}
	if changed == 0 {
		return "already root", true, nil
	}

	r.reboot = true
	return fmt.Sprintf("%d lines changed", changed), false, nil
}

func deAmazon(r *run) (string, bool, error) {
	p := r.cfg.Profile
	disable, enable := services.Set(p.Disable), services.Set(p.Enable)

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
		return fmt.Sprintf("%d already disabled and none running", len(p.Disable)), true, nil
	}

	r.reboot = true
	return fmt.Sprintf("%d rc entries changed, %d stopped", changed, len(stopped)), false, nil
}

// adb push unlinks the destination before creating it.
func (r *run) writeInPlace(path string, data []byte) error {
	const staged = "/data/local/tmp/echolocal-staged"

	if err := r.d.WriteFile(staged, data, 0o600); err != nil {
		return err
	}
	defer func() { _, _ = r.d.Shell("rm -f " + staged) }()

	_, err := r.d.Shell(fmt.Sprintf("cat %s > %s", staged, path))
	return err
}

func (r *run) stopServices() ([]string, error) {
	props, err := r.d.Shell("getprop")
	if err != nil {
		return nil, err
	}
	running := runningServices(props)

	var stopped []string
	for _, name := range r.cfg.Profile.Disable {
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

// getprop lines look like [init.svc.mixer]: [running].
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

func (r *run) patchAdbd(dir string) (string, error) {
	adbd := r.system().Adbd
	path := dir + "/" + adbd.Path

	current, err := r.peek(path, adbd.Offset, len(adbd.Before))
	if err != nil {
		return "", err
	}

	switch {
	case bytes.Equal(current, adbd.After):
		return "already patched", nil
	case !bytes.Equal(current, adbd.Before):
		return "", fmt.Errorf("%s reads %x at %d, which is neither the stock bytes nor ours",
			path, current, adbd.Offset)
	}

	if err := r.poke(path, adbd.Offset, adbd.After); err != nil {
		return "", err
	}
	return "patched", nil
}

func (r *run) peek(path string, at int64, n int) ([]byte, error) {
	raw, err := r.d.Shell(
		fmt.Sprintf(
			"toybox dd if=%s bs=1 skip=%d count=%d 2>/dev/null | od -An -tx1 | tr -d ' \\n'",
			path,
			at,
			n,
		),
	)
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

// The dd on PATH in recovery is built without conv.
func (r *run) poke(path string, at int64, b []byte) error {
	var escaped strings.Builder
	for _, c := range b {
		fmt.Fprintf(&escaped, `\%03o`, c)
	}

	_, err := r.d.Shell(
		fmt.Sprintf("printf '%s' | toybox dd of=%s bs=1 seek=%d conv=notrunc 2>/dev/null",
			escaped.String(), path, at),
	)
	return err
}

// Recovery's fstab mounts the running slot's system partition here read-write.
const systemMount = "/system_root"

// Mounting the system partition twice is refused as busy.
func (r *run) mountSystem() (string, error) {
	_, _ = r.d.Shell("umount " + systemMount + " 2>/dev/null")

	if _, err := r.d.Shell("mount " + systemMount); err != nil {
		return "", fmt.Errorf("mounting %s: %w", systemMount, err)
	}
	return systemMount, nil
}

// A Fire OS OTA writes the other slot and switches to it.
func (r *run) neuterOTA(at string) error {
	path := at + "/system/build.prop"

	current, err := r.d.ReadFile(path)
	if err != nil {
		return err
	}
	layout := r.system()
	if layout.OTAKey == "" {
		return nil
	}
	return r.writeInPlace(path, setProp(current, layout.OTAKey, layout.OTAValue))
}

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

func (r *run) systemNode() (string, error) {
	if r.board().Slotted && r.state.slot == "" {
		return "", errUnknownSlot
	}
	return sysimg.Node(r.state.slot), nil
}

func (r *run) setProps(at string, layout sysimg.Layout) (string, error) {
	if len(layout.Props) == 0 {
		return "", nil
	}
	path := at + "/" + layout.PropsPath

	current, err := r.d.ReadFile(path)
	if err != nil {
		return "", err
	}

	keys := slices.Sorted(maps.Keys(layout.Props))
	for _, key := range keys {
		current = setProp(current, key, layout.Props[key])
	}
	if err := r.writeInPlace(path, current); err != nil {
		return "", err
	}
	return layout.PropsPath, nil
}

func (r *run) takeService(at string, layout sysimg.Layout) (string, error) {
	if layout.ServiceRC == "" {
		return "", nil
	}

	b := r.board()
	rc := fmt.Sprintf(
		"service %s %s\n    class main\n    priority -20\n    user root\n    group root\n",
		b.ServiceName,
		b.Service,
	)

	if err := r.writeInPlace(at+"/"+layout.ServiceRC, []byte(rc)); err != nil {
		return "", err
	}
	return b.ServiceName, nil
}
