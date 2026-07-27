// Package device wraps gadb with the shell semantics this device needs: commands carry their
// own exit status, and output is normalised from CRLF.
package device

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/electricbubble/gadb"
)

const rcMarker = "__echolocal_rc="

// Device is one connected Echo Dot, with a root adbd.
type Device struct {
	d gadb.Device
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

// Connect selects a device, or the only one present when serial is empty.
func Connect(serial string) (*Device, error) {
	c, err := gadb.NewClient()
	if err != nil {
		return nil, fmt.Errorf("device: no adb server on 127.0.0.1:5037 (%w); start one with `adb start-server`", err)
	}
	devices, err := c.DeviceList()
	if err != nil {
		return nil, fmt.Errorf("device: listing devices: %w", err)
	}

	switch {
	case len(devices) == 0:
		return nil, errors.New("device: no device connected; check the USB cable and `adb devices`")
	case serial != "":
		for _, d := range devices {
			if d.Serial() == serial {
				return &Device{d: d}, nil
			}
		}
		return nil, fmt.Errorf("device: no device with serial %q", serial)
	case len(devices) > 1:
		serials := make([]string, 0, len(devices))
		for _, d := range devices {
			serials = append(serials, d.Serial())
		}
		return nil, fmt.Errorf("device: %d devices connected, pick one with --serial: %s",
			len(devices), strings.Join(serials, ", "))
	}

	dev := &Device{d: devices[0]}

	// Everything the installer does needs root, so establish it here rather than failing
	// partway through a /system edit.
	root, err := dev.IsRoot()
	if err != nil {
		return nil, err
	}
	if !root {
		return nil, errors.New("device: adbd is not running as root; unlock and root the device first")
	}
	return dev, nil
}

func (d *Device) Serial() string { return d.d.Serial() }

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

func (d *Device) shell(cmd string) (string, int, error) {
	raw, err := d.d.RunShellCommandWithBytes(cmd + "; echo " + rcMarker + "$?")
	if err != nil {
		return "", 0, fmt.Errorf("device: running %q: %w", cmd, err)
	}
	out, code, err := splitRC(string(raw))
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

// ReadFile pulls a file into memory.
func (d *Device) ReadFile(path string) ([]byte, error) {
	var buf bytes.Buffer
	if err := d.d.Pull(path, &buf); err != nil {
		return nil, fmt.Errorf("device: pulling %s: %w", path, err)
	}
	return buf.Bytes(), nil
}

// PullFile copies a file off the device.
func (d *Device) PullFile(remote, local string) error {
	f, err := os.Create(local)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := d.d.Pull(remote, f); err != nil {
		return fmt.Errorf("device: pulling %s: %w", remote, err)
	}
	return f.Sync()
}

// PushFile copies a file onto the device and sets its mode.
//
// A push never lands the mode we ask for: creating a file widens the owner bits to group and
// other, and overwriting one yields 0644. The chmod is what actually sets it, and without it a
// re-install leaves a binary init cannot exec.
func (d *Device) PushFile(local, remote string, mode os.FileMode) error {
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := d.d.PushFile(f, remote); err != nil {
		return fmt.Errorf("device: pushing %s: %w", remote, err)
	}

	_, err = d.Shell(fmt.Sprintf("chmod %o %s", mode.Perm(), quote(remote)))
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

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
