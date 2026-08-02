package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

// board writes the files a biscuit has, with the values it reported, so the parsing is tested against
// what the device actually says rather than against what this machine happens to have.
func board(t *testing.T) Reader {
	t.Helper()
	root := t.TempDir()

	for path, body := range map[string]string{
		"sys/class/thermal/thermal_zone0/type": "mtktswmt\n",
		"sys/class/thermal/thermal_zone0/temp": "46000\n",
		"sys/class/thermal/thermal_zone1/type": "mtktscpu\n",
		"sys/class/thermal/thermal_zone1/temp": "45500\n",
		"sys/class/thermal/thermal_zone2/type": "mtkts1\n",
		"sys/class/thermal/thermal_zone2/temp": "43400\n",
		"sys/devices/system/cpu/present":       "0-3\n",
		"sys/devices/system/cpu/online":        "0-1\n",
		"proc/loadavg":                         "7.40 7.30 7.25 5/623 6841\n",
		"proc/meminfo":                         "MemTotal:         482956 kB\nMemFree:           25708 kB\nMemAvailable:     247308 kB\n",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Reader{Root: root}
}

func TestTemperaturesComeBackByZoneNameInDegrees(t *testing.T) {
	temps := board(t).Temperatures()

	for zone, want := range map[string]float64{"mtktscpu": 45.5, "mtktswmt": 46, "mtkts1": 43.4} {
		got, ok := temps[zone]
		if !ok {
			t.Errorf("%s missing", zone)
			continue
		}
		if got != want {
			t.Errorf("%s = %v°C, want %v", zone, got, want)
		}
	}
	if len(temps) != 3 {
		t.Errorf("found %d zones, want 3", len(temps))
	}
}

// Present and online differ on this hardware, and that difference is the reason to report both.
func TestCoresReportsPresentAndOnline(t *testing.T) {
	present, online := board(t).Cores()

	if !present.Known || present.Value != 4 {
		t.Errorf("present = %+v, want 4", present)
	}
	if !online.Known || online.Value != 2 {
		t.Errorf("online = %+v, want 2", online)
	}
}

func TestCoreListForms(t *testing.T) {
	for list, want := range map[string]float64{
		"0-3":     4,
		"0-1":     2,
		"0":       1,
		"0-1,3":   3,
		"0,2,4-5": 4,
	} {
		root := t.TempDir()
		path := filepath.Join(root, "sys/devices/system/cpu/online")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(list+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		if got := (Reader{Root: root}).count("sys/devices/system/cpu/online"); got.Value != want {
			t.Errorf("%q counted %v, want %v", list, got.Value, want)
		}
	}
}

func TestLoadAndMemory(t *testing.T) {
	r := board(t)

	one, five := r.Load()
	if !one.Known || one.Value != 7.40 || five.Value != 7.30 {
		t.Errorf("load = %v, %v, want 7.40, 7.30", one.Value, five.Value)
	}

	// Available, not free: free counts only untouched pages and reads alarmingly low on a machine that
	// is working properly — 25 MB here against 247 MB actually available.
	available, total := r.Memory()
	if !available.Known || available.Value != 247308 {
		t.Errorf("available = %+v, want 247308", available)
	}
	if !total.Known || total.Value != 482956 {
		t.Errorf("total = %+v, want 482956", total)
	}
}

// A board without a sensor reports nothing rather than zero, so Home Assistant shows unknown instead of
// a plausible lie.
func TestMissingFilesAreUnknownNotZero(t *testing.T) {
	r := Reader{Root: t.TempDir()}

	if temps := r.Temperatures(); len(temps) != 0 {
		t.Errorf("found %d zones on an empty root", len(temps))
	}
	present, online := r.Cores()
	one, _ := r.Load()
	available, _ := r.Memory()

	for name, got := range map[string]Reading{
		"present": present, "online": online, "load": one, "available": available,
	} {
		if got.Known {
			t.Errorf("%s reported %v, want unknown", name, got.Value)
		}
	}
}
