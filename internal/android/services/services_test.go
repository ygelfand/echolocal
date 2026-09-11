package services

import (
	"strings"
	"testing"
)

const rc = `on post-fs-data
    mkdir /data/misc/audio 0770 audio audio

service mixer /system/bin/mixer
    class main
    user audio

service acepowerd /system/bin/ace_powerd
    class main

service bugreport /system/bin/dumpstate -d -p -B \
        -o /data/log/bugreport
    class main
    disabled
    oneshot

service otad /system/bin/otad
    class late_start

on property:com.amazon.adepd.run=1
    start mixer
    start acepowerd
`

func TestDisableOnlyTheNamed(t *testing.T) {
	out, changed := Disable(rc, map[string]bool{"mixer": true, "otad": true})

	if len(changed) != 3 {
		t.Fatalf("changed %v, want mixer, otad and mixer's explicit start", changed)
	}

	for _, want := range []string{
		"service mixer /system/bin/mixer\n    disabled\n",
		"service otad /system/bin/otad\n    disabled\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing:\n%s\ngot:\n%s", want, out)
		}
	}

	// A service nobody named keeps its definition exactly.
	if !strings.Contains(out, "service acepowerd /system/bin/ace_powerd\n    class main\n") {
		t.Error("acepowerd was changed")
	}
}

// Running an install twice must not stack keywords, and a service Amazon already ships disabled is
// already where we want it.
func TestDisableIsIdempotent(t *testing.T) {
	once, _ := Disable(rc, map[string]bool{"mixer": true})
	twice, changed := Disable(once, map[string]bool{"mixer": true})

	if len(changed) != 0 {
		t.Errorf("second run changed %v", changed)
	}
	if once != twice {
		t.Error("a second run rewrote the file")
	}
	if n := strings.Count(twice, "    disabled"); n != 2 {
		t.Errorf("%d disabled lines, want the one we added and the one bugreport had", n)
	}
}

// The keyword only stops a service starting with its class. An explicit start in an on-block runs it
// regardless, so that has to go too — on this device most of the services we disable have one.
func TestDisableCommentsOutExplicitStarts(t *testing.T) {
	out, changed := Disable(rc, map[string]bool{"mixer": true})

	if !strings.Contains(out, "#    start mixer") {
		t.Errorf("the explicit start was left live:\n%s", out)
	}
	if !strings.Contains(out, "\n    start acepowerd") {
		t.Error("a start for a service nobody named was commented out")
	}

	var sawStart bool
	for _, c := range changed {
		if c == "start mixer" {
			sawStart = true
		}
	}
	if !sawStart {
		t.Errorf("changed %v, want the commented start reported", changed)
	}
}

// The keyword has to land after the whole command, not in the middle of a wrapped one.
func TestDisableFollowsAWrappedCommand(t *testing.T) {
	out, changed := Disable(rc, map[string]bool{"bugreport": true, "mixer": true})

	for _, name := range changed {
		if name == "bugreport" {
			t.Error("bugreport already carries disabled and was changed again")
		}
	}
	if !strings.Contains(out, "-o /data/log/bugreport\n    class main") {
		t.Errorf("the wrapped command was broken:\n%s", out)
	}
}

// dhcpcd ships disabled and nothing starts it, so init has to. Only the named service loses the
// keyword, and a service that never had one is left alone.
func TestEnableDropsTheKeyword(t *testing.T) {
	const rc = `service dhcpcd-wlan0 /system/bin/dhcpcd wlan0 -AdLK
    class main
    disabled

service dhcpcd-eth0 /system/bin/dhcpcd eth0 -AdLK
    class main
    disabled
`

	out, changed := Enable(rc, map[string]bool{"dhcpcd-wlan0": true})

	if len(changed) != 1 || changed[0] != "dhcpcd-wlan0" {
		t.Fatalf("changed %v, want just dhcpcd-wlan0", changed)
	}
	if !strings.Contains(out, "wlan0 -AdLK\n    class main\n\nservice") {
		t.Errorf("wlan0 kept its disabled:\n%s", out)
	}
	if !strings.Contains(out, "eth0 -AdLK\n    class main\n    disabled\n") {
		t.Errorf("eth0 lost its disabled:\n%s", out)
	}

	if _, again := Enable(out, map[string]bool{"dhcpcd-wlan0": true}); len(again) != 0 {
		t.Errorf("a second run changed %v", again)
	}
}

// echod takes over a service Amazon declares unprivileged and needs root for the hardware. Only that
// service loses its user and group; the ones around it keep theirs.
func TestAsRootDropsOnlyThatServicesUser(t *testing.T) {
	const takeover = `service ledcontroller /system/bin/ledcontroller
   class main
   user ledcontroller
   group aipc dbus als

service boot2_anim_start /system/bin/start_animation.sh anim_start_phase2
    user ledcontroller
    group dbus
    disabled
`

	out, changed := AsRoot(takeover, "ledcontroller")

	if len(changed) != 3 {
		t.Fatalf("changed %v, want the seclabel added and user and group dropped", changed)
	}
	if !strings.Contains(out, "service ledcontroller /system/bin/ledcontroller\n    seclabel "+Domain+"\n   class main\n\nservice") {
		t.Errorf("ledcontroller did not come out root in a domain:\n%s", out)
	}
	if !strings.Contains(out, "anim_start_phase2\n    user ledcontroller\n    group dbus\n") {
		t.Errorf("the next service lost its user:\n%s", out)
	}

	if _, again := AsRoot(out, "ledcontroller"); len(again) != 0 {
		t.Errorf("a second run changed %v", again)
	}
}
