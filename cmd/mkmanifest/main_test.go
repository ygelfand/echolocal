package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ygelfand/echolocal/internal/update"
)

// deployed is the manifest as every device released before the binaries map existed parses it. Those
// devices update through these four fields and nothing else, so a release that stops filling them in
// leaves every one of them stuck on the build it is running.
type deployed struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

func write(t *testing.T) (deployed, update.Manifest) {
	t.Helper()
	dir := t.TempDir()

	for name, body := range map[string]string{"echod-arm64": "sixty four", "echod-arm": "thirty two"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	out := filepath.Join(dir, "manifest.json")
	err := run(update.Manifest{Version: "0.0.7"}, "https://example/download/0.0.7", map[string]string{
		"arm64": filepath.Join(dir, "echod-arm64"),
		"arm":   filepath.Join(dir, "echod-arm"),
	}, out)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	var old deployed
	var now update.Manifest
	if err := json.Unmarshal(encoded, &old); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &now); err != nil {
		t.Fatal(err)
	}
	return old, now
}

func TestADeployedDeviceCanStillReadIt(t *testing.T) {
	old, now := write(t)

	if old.Version == "" || old.URL == "" || len(old.SHA256) != 64 || old.Size <= 0 {
		t.Fatalf("a device on the old manifest reads %+v, which it will refuse", old)
	}

	// Those fields have to be the arm64 build: every deployed device is one.
	arm64 := now.Binaries["arm64"]
	if old.URL != arm64.URL || old.SHA256 != arm64.SHA256 || old.Size != arm64.Size {
		t.Errorf("the top-level fields describe %+v, want the arm64 build %+v", old, arm64)
	}
}

func TestEachArchitectureGetsItsOwnBuild(t *testing.T) {
	_, now := write(t)

	if err := now.Valid(); err != nil {
		t.Fatal(err)
	}

	for arch, want := range map[string]string{
		"arm64": "https://example/download/0.0.7/echod-arm64",
		"arm":   "https://example/download/0.0.7/echod-arm",
	} {
		b, err := now.For(arch)
		if err != nil {
			t.Errorf("%s: %v", arch, err)
			continue
		}
		if b.URL != want {
			t.Errorf("%s: offered %s, want %s", arch, b.URL, want)
		}
	}

	if now.Binaries["arm64"].SHA256 == now.Binaries["arm"].SHA256 {
		t.Error("both architectures were measured as the same file")
	}
}
