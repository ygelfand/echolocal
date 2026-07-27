package device

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tmpDir = "/data/local/tmp"

// Live tests need a Dot on USB, so they only run when asked for:
//
//	ECHOLOCAL_DEVICE=1 go test ./internal/device/ -run Live -v
func liveDevice(t *testing.T) *Device {
	t.Helper()
	if os.Getenv("ECHOLOCAL_DEVICE") == "" {
		t.Skip("set ECHOLOCAL_DEVICE=1 to run against a connected device")
	}
	d, err := Connect("")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return d
}

func TestLiveShellExitStatus(t *testing.T) {
	d := liveDevice(t)
	t.Logf("serial=%s", d.Serial())

	if _, err := d.Shell("true"); err != nil {
		t.Errorf("true: unexpected error: %v", err)
	}

	// The whole reason this package exists: a failing command must not look successful.
	_, err := d.Shell("false")
	var derr *Error
	if !errors.As(err, &derr) {
		t.Fatalf("false: want *Error, got %v", err)
	}
	if derr.Code == 0 {
		t.Errorf("false: exit code = 0, want non-zero")
	}

	out, code, err := d.ShellCode("ls /definitely-not-here")
	if err != nil {
		t.Fatalf("ShellCode: %v", err)
	}
	if code == 0 {
		t.Errorf("missing path: exit code = 0, want non-zero")
	}
	if strings.Contains(out, "\r") {
		t.Errorf("output still contains CR: %q", out)
	}
}

func TestLiveDeviceFacts(t *testing.T) {
	d := liveDevice(t)

	root, err := d.IsRoot()
	if err != nil || !root {
		t.Fatalf("IsRoot() = %v, %v; want true", root, err)
	}

	sdk, err := d.Getprop("ro.build.version.sdk")
	if err != nil {
		t.Fatalf("Getprop: %v", err)
	}
	if sdk != "22" {
		t.Errorf("ro.build.version.sdk = %q, want 22", sdk)
	}

	up, err := d.Uptime()
	if err != nil || up <= 0 {
		t.Errorf("Uptime() = %v, %v; want > 0", up, err)
	}

	label, err := d.Label("/system/app")
	if err != nil {
		t.Fatalf("Label: %v", err)
	}
	if label != "u:object_r:system_file:s0" {
		t.Errorf("/system/app label = %q, want u:object_r:system_file:s0", label)
	}
}

func TestLivePushPull(t *testing.T) {
	d := liveDevice(t)

	local := filepath.Join(t.TempDir(), "probe")
	want := []byte("echolocal push/pull probe\n")
	if err := os.WriteFile(local, want, 0o644); err != nil {
		t.Fatal(err)
	}

	remote := tmpDir + "/echolocal-probe"
	if err := d.PushFile(local, remote, 0o755); err != nil {
		t.Fatalf("PushFile: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Shell("rm -f " + quote(remote)) })

	got, err := d.ReadFile(remote)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("round trip = %q, want %q", got, want)
	}

	// Binary integrity matters more than text: the log is pulled through the sync service,
	// not the pty, so it must survive byte for byte.
	if exists, err := d.Exists("/system/app/echod/echod"); err == nil && exists {
		bin, err := d.ReadFile("/system/app/echod/echod")
		if err != nil {
			t.Fatalf("reading echod: %v", err)
		}
		if len(bin) < 1<<20 || string(bin[:4]) != "\x7fELF" {
			t.Errorf("echod pulled as %d bytes starting %q, want an ELF", len(bin), firstBytes(bin))
		}
	}
}

func firstBytes(b []byte) string {
	if len(b) > 4 {
		b = b[:4]
	}
	return string(b)
}
