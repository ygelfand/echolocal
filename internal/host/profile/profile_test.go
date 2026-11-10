package profile

import (
	"testing"

	"github.com/ygelfand/echolocal/internal/host/sysimg"
)

// Fire OS 6 on biscuit has no package manager.
func TestBiscuitHidesNothing(t *testing.T) {
	if len(biscuit.Hide) != 0 {
		t.Errorf("biscuit hides %d packages: %v", len(biscuit.Hide), biscuit.Hide)
	}
}

func TestEveryProfileNamesAService(t *testing.T) {
	for device, p := range profiles {
		if p.Board.ServiceName == "" || p.Board.Service == "" {
			t.Errorf("%s names no service for echod to take over", device)
		}
		if _, err := For(device); err != nil {
			t.Errorf("%s has a complete profile and was still refused: %v", device, err)
		}
	}

	if _, err := For("sailfish"); err == nil {
		t.Error("a device nobody has heard of resolved a profile")
	}
}

func TestHiddenListsAreDistinct(t *testing.T) {
	for device, p := range profiles {
		seen := make(map[string]bool, len(p.Hide))
		for _, pkg := range p.Hide {
			if seen[pkg.Name] {
				t.Errorf("%s hides %s twice", device, pkg.Name)
			}
			seen[pkg.Name] = true

			if pkg.Reason == "" {
				t.Errorf("%s hides %s with no reason", device, pkg.Name)
			}
		}
	}
}

// default.prop carries the build fingerprint: biscuit's copy on a radar would misreport it.
func TestRadarEditsItsOwnDefaultProp(t *testing.T) {
	for _, p := range radar.System.Patches {
		if p.Path == sysimg.DefaultProp.Path {
			t.Fatalf("radar replaces %s with %s", p.Path, p.File)
		}
	}
	if radar.System.PropsPath != sysimg.DefaultProp.Path {
		t.Errorf("radar edits %q, want %q", radar.System.PropsPath, sysimg.DefaultProp.Path)
	}
	for key, want := range map[string]string{"ro.secure": "0", "ro.adb.secure": "0"} {
		if got := radar.System.Props[key]; got != want {
			t.Errorf("radar sets %s=%q, want %q", key, got, want)
		}
	}
}

func TestRadarResolves(t *testing.T) {
	p, err := For("radar_puffin")
	if err != nil {
		t.Fatal(err)
	}
	if p.Board.Codename != "radar" || p.Boot.Device != "radar_puffin" {
		t.Errorf("radar_puffin resolved to board %s with a %s image", p.Board, p.Boot.Device)
	}
}
