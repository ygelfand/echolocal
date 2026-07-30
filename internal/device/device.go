// Package device drives a connected Echo Dot through the adb binary, with the shell semantics this
// device needs: commands carry their own exit status, and output is normalised from CRLF.
//
// Shelling out rather than speaking the protocol: every Go adb library talks to the adb server rather
// than to the device, so platform-tools is a prerequisite either way. Given that, the binary is worth
// more than a library — it brings wait-for-device, wait-for-recovery, reboot and the sync protocol,
// all of which a library makes us reimplement.
package device

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Binary is what has to be on PATH.
const Binary = "adb"

const rcMarker = "__echolocal_rc="

// PlatformTools is where to get adb, for an error worth reading.
const PlatformTools = "https://developer.android.com/tools/releases/platform-tools"

// Require reports whether adb is available, and says what to install when it is not. Every entry
// point checks this first: a missing binary should be one clear sentence, not a failure part way
// through talking to a device.
func Require() error {
	if _, err := exec.LookPath(Binary); err != nil {
		return fmt.Errorf("adb is not on PATH: install Android platform-tools and try again\n"+
			"  %s\n"+
			"  macOS: brew install --cask android-platform-tools\n"+
			"  Debian/Ubuntu: apt install android-sdk-platform-tools", PlatformTools)
	}
	return nil
}

// Device is one connected Echo Dot, addressed by serial so a second device cannot be caught by
// accident once one has been chosen.
type Device struct {
	serial string
}

// Error is a shell command that ran and failed.
type Error struct {
	Cmd    string
	Output string
	Code   int
}

func (e *Error) Error() string {
	out := strings.TrimSpace(e.Output)
	if out == "" {
		return fmt.Sprintf("device: %q exited %d", e.Cmd, e.Code)
	}
	return fmt.Sprintf("device: %q exited %d: %s", e.Cmd, e.Code, out)
}

// Info identifies a connected device well enough to choose between several.
type Info struct {
	Serial  string
	Model   string
	Product string
}

func (i Info) String() string {
	switch {
	case i.Model != "" && i.Product != "":
		return fmt.Sprintf("%s (%s)", i.Model, i.Product)
	case i.Model != "":
		return i.Model
	}
	return i.Serial
}

// State is what adb says about a device. Only two matter here: a device that can be installed to, and
// one in recovery, which is where the boot image is written from.
const (
	StateOnline   = "device"
	StateRecovery = "recovery"
)

// List returns the devices adb can actually talk to. Offline and unauthorized entries are skipped:
// they show up in adb's listing but every command against them fails.
func List() ([]Info, error) {
	serials, err := listing(StateOnline)
	if err != nil {
		return nil, err
	}

	out := make([]Info, 0, len(serials))
	for _, serial := range serials {
		d := &Device{serial: serial}
		model, _ := d.Getprop("ro.product.model")
		product, _ := d.Getprop("ro.product.device")
		out = append(out, Info{Serial: serial, Model: model, Product: product})
	}
	return out, nil
}

// listing is the serials in any of the given states. `adb devices` reports one per line as
// "serial<tab>state" after a header.
func listing(states ...string) ([]string, error) {
	out, err := run(context.Background(), "devices")
	if err != nil {
		return nil, err
	}

	var serials []string
	for line := range strings.SplitSeq(out, "\n") {
		serial, state, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		for _, want := range states {
			if strings.TrimSpace(state) == want {
				serials = append(serials, serial)
			}
		}
	}
	return serials, nil
}

// Connect selects a device an install can use: online, with a root adbd.
func Connect(serial string) (*Device, error) {
	d, err := Attach(serial)
	if err != nil {
		return nil, err
	}
	return rooted(d)
}

// Attach is Connect without the root check, for the one job that runs before root exists: writing the
// boot image that grants it. Everything else takes Connect and is refused early.
func Attach(serial string) (*Device, error) {
	return attach(serial, StateOnline)
}

// AttachAny also accepts a device in recovery, which is where the boot partition is written and whose
// adbd is already root.
func AttachAny(serial string) (*Device, error) {
	return attach(serial, StateOnline, StateRecovery)
}

func attach(serial string, states ...string) (*Device, error) {
	if err := Require(); err != nil {
		return nil, err
	}
	serials, err := listing(states...)
	if err != nil {
		return nil, err
	}

	switch {
	case len(serials) == 0:
		return nil, errors.New("device: no device connected; check the USB cable and `adb devices`")
	case serial != "":
		for _, s := range serials {
			if s == serial {
				return &Device{serial: s}, nil
			}
		}
		return nil, fmt.Errorf("device: no device with serial %q", serial)
	case len(serials) > 1:
		return nil, fmt.Errorf("device: %d devices connected, pick one with --serial: %s",
			len(serials), strings.Join(serials, ", "))
	}
	return &Device{serial: serials[0]}, nil
}

// rooted refuses a device that cannot do what an install needs, rather than failing partway
// through a /system edit.
func rooted(d *Device) (*Device, error) {
	root, err := d.IsRoot()
	if err != nil {
		return nil, err
	}
	if !root {
		return nil, errors.New("device: adbd is not running as root; write the boot image first (echoctl install --flash-only)")
	}
	return d, nil
}

func (d *Device) Serial() string { return d.serial }

// Reboot restarts the device. An empty target is Android; "recovery" is TWRP, which is the only place
// the boot partition can be written.
//
// The device goes away as this runs, so a caller waits for it to come back with Wait.
func (d *Device) Reboot(target string) error {
	args := []string{"reboot"}
	if target != "" {
		args = append(args, target)
	}
	_, err := d.run(context.Background(), args...)
	return err
}

// Wait blocks until the device is back in one of the given states, or ctx is done. It then confirms a
// shell answers, because adb reports a device as present a moment before it will run anything.
func (d *Device) Wait(ctx context.Context, states ...string) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		serials, err := listing(states...)
		if err == nil {
			for _, s := range serials {
				if s != d.serial {
					continue
				}
				if _, err := d.Shell("true"); err == nil {
					return nil
				}
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("device: waiting for %s in %v: %w", d.serial, states, ctx.Err())
		case <-tick.C:
		}
	}
}

// WaitBooted waits for Android to finish coming up, not merely to answer: an install that starts
// while the framework is still starting sees services that are not there yet.
func (d *Device) WaitBooted(ctx context.Context) error {
	if err := d.Wait(ctx, StateOnline); err != nil {
		return err
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		if done, _ := d.Getprop("sys.boot_completed"); done == "1" {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("device: %s did not finish booting: %w", d.serial, ctx.Err())
		case <-tick.C:
		}
	}
}

// Shell runs a command and fails if it exits non-zero.
func (d *Device) Shell(cmd string) (string, error) {
	out, code, err := d.shell(cmd)
	if err != nil {
		return out, err
	}
	if code != 0 {
		return out, &Error{Cmd: cmd, Output: out, Code: code}
	}
	return out, nil
}

// ShellCode returns the exit status instead of failing on it, for checks where non-zero is an
// answer rather than a problem.
func (d *Device) ShellCode(cmd string) (string, int, error) {
	return d.shell(cmd)
}

// shell appends its own exit-status marker rather than trusting adb to propagate one, which older
// adb does not.
func (d *Device) shell(cmd string) (string, int, error) {
	raw, err := d.run(context.Background(), "shell", cmd+"; echo "+rcMarker+"$?")
	if err != nil {
		return "", 0, fmt.Errorf("device: running %q: %w", cmd, err)
	}
	out, code, err := splitRC(raw)
	if err != nil {
		return out, 0, fmt.Errorf("device: running %q: %w", cmd, err)
	}
	return out, code, nil
}

// splitRC separates output from the trailing exit-status marker.
func splitRC(raw string) (string, int, error) {
	out := strings.ReplaceAll(raw, "\r\n", "\n")

	i := strings.LastIndex(out, rcMarker)
	if i < 0 {
		return out, 0, fmt.Errorf("no %s marker in output %q", rcMarker, out)
	}
	code, err := strconv.Atoi(strings.TrimSpace(out[i+len(rcMarker):]))
	if err != nil {
		return out, 0, fmt.Errorf("unparseable exit status in %q", out[i:])
	}
	return strings.TrimSuffix(out[:i], "\n"), code, nil
}

// Exists reports whether a path is present.
func (d *Device) Exists(path string) (bool, error) {
	_, code, err := d.ShellCode("ls -d " + quote(path))
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

// IsSymlink reports whether a path is a symbolic link.
func (d *Device) IsSymlink(path string) (bool, error) {
	_, code, err := d.ShellCode("[ -L " + quote(path) + " ]")
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

// ReadFile reads a file into memory through exec-out, which is the only shell that does not translate
// line endings. Reading a boot partition through `adb shell cat` corrupts it silently.
func (d *Device) ReadFile(path string) ([]byte, error) {
	cmd := d.command(context.Background(), "exec-out", "cat "+quote(path))

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("device: reading %s: %w", path, err)
	}
	return out, nil
}

// PullFile copies a file off the device.
func (d *Device) PullFile(remote, local string) error {
	if _, err := d.run(context.Background(), "pull", remote, local); err != nil {
		return fmt.Errorf("device: pulling %s: %w", remote, err)
	}
	return nil
}

// Stream writes bytes into a file on the device through exec-in, which pipes stdin to a command
// untranslated and without the sync protocol. `adb shell` is not an alternative: it mangles binary on
// the way in.
func (d *Device) Stream(remote string, data []byte) error {
	cmd := d.command(context.Background(), "exec-in", fmt.Sprintf("dd of=%s bs=1M", quote(remote)))
	cmd.Stdin = bytes.NewReader(data)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("device: streaming %s: %w: %s", remote, err, strings.Join(strings.Fields(string(out)), " "))
	}
	return nil
}

// WriteFile creates a file on the device with the given contents and mode.
func (d *Device) WriteFile(remote string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp("", "echoctl")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return d.PushFile(tmp.Name(), remote, mode)
}

// PushFile copies a file onto the device and sets its mode.
//
// A push never lands the mode we ask for: creating a file widens the owner bits to group and
// other, and overwriting one yields 0644. The chmod is what actually sets it, and without it a
// re-install leaves a binary init cannot exec.
func (d *Device) PushFile(local, remote string, mode os.FileMode) error {
	if _, err := d.run(context.Background(), "push", local, remote); err != nil {
		return fmt.Errorf("device: pushing %s: %w", remote, err)
	}

	_, err := d.Shell(fmt.Sprintf("chmod %o %s", mode.Perm(), quote(remote)))
	return err
}

// Getprop reads a system property. A property that is unset reads as empty, not an error.
func (d *Device) Getprop(name string) (string, error) {
	out, err := d.Shell("getprop " + quote(name))
	return strings.TrimSpace(out), err
}

// Setprop writes a system property.
func (d *Device) Setprop(name, value string) error {
	_, err := d.Shell(fmt.Sprintf("setprop %s %s", quote(name), quote(value)))
	return err
}

// Label reads a path's SELinux label.
func (d *Device) Label(path string) (string, error) {
	out, err := d.Shell("ls -Zd " + quote(path))
	if err != nil {
		return "", err
	}
	for f := range strings.FieldsSeq(out) {
		if strings.HasPrefix(f, "u:object_r:") {
			return f, nil
		}
	}
	return "", fmt.Errorf("device: no SELinux label in %q", strings.TrimSpace(out))
}

// Chcon sets a path's SELinux label.
func (d *Device) Chcon(label, path string) error {
	_, err := d.Shell(fmt.Sprintf("chcon %s %s", quote(label), quote(path)))
	return err
}

// IsRoot reports whether the adb shell is uid 0. `id -u` is unsupported, so `id` is parsed.
func (d *Device) IsRoot() (bool, error) {
	out, err := d.Shell("id")
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "uid=0("), nil
}

// Uptime is seconds since boot.
func (d *Device) Uptime() (float64, error) {
	out, err := d.Shell("cat /proc/uptime")
	if err != nil {
		return 0, err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(out), " ")
	v, err := strconv.ParseFloat(first, 64)
	if err != nil {
		return 0, fmt.Errorf("device: unparseable uptime %q", out)
	}
	return v, nil
}

// command builds an adb invocation aimed at this device.
func (d *Device) command(ctx context.Context, args ...string) *exec.Cmd {
	if d.serial != "" {
		args = append([]string{"-s", d.serial}, args...)
	}
	return exec.CommandContext(ctx, Binary, args...)
}

func (d *Device) run(ctx context.Context, args ...string) (string, error) {
	return output(d.command(ctx, args...))
}

func run(ctx context.Context, args ...string) (string, error) {
	return output(exec.CommandContext(ctx, Binary, args...))
}

// output reports stdout and stderr together, since adb explains refusals on stderr. Errors carry that
// output on one line: adb's is several, and a multi-line error wrecks any progress display it lands in.
func output(cmd *exec.Cmd) (string, error) {
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		said := strings.Join(strings.Fields(text), " ")
		return text, fmt.Errorf("%s: %w: %s", strings.Join(cmd.Args, " "), err, said)
	}
	return text, nil
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
