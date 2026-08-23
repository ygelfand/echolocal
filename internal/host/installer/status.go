package installer

import (
	"strconv"
	"strings"
	"time"

	"github.com/ygelfand/echolocal/internal/host/device"
	"github.com/ygelfand/echolocal/internal/layout"
)

// State is what echod's installation looks like on a device.
type State struct {
	Serial  string
	Model   string
	Product string
	SDK     string
	Uptime  float64

	// Name is what Home Assistant calls the device, and Provisioned whether it has a key.
	Name        string
	Provisioned bool

	Installed       bool
	LinkTarget      string
	HaveBackup      bool
	Version         string
	AndroidMedia    bool
	PryonInstalled  bool
	PryonConfigured bool
	APIListening    bool

	ServiceState string
	AgentState   string
	StartedAt    string
}

// RunningFor is how long the current echod process has been up, from the uptime it recorded at
// start against the device's uptime now.
func (s State) RunningFor() (time.Duration, bool) {
	started, err := strconv.ParseFloat(s.StartedAt, 64)
	if err != nil || started <= 0 || s.Uptime < started {
		return 0, false
	}
	return time.Duration((s.Uptime - started) * float64(time.Second)), true
}

// ReadState reads installation and runtime state without changing anything.
func ReadState(d *device.Device) (State, error) {
	s := State{Serial: d.Serial()}

	s.Model, _ = d.Getprop("ro.product.model")
	s.Product, _ = d.Getprop("ro.product.device")
	s.SDK, _ = d.Getprop("ro.build.version.sdk")
	s.Uptime, _ = d.Uptime()

	link, err := d.IsSymlink(layout.Service)
	if err != nil {
		return s, err
	}
	if link {
		target, err := d.Shell("readlink " + layout.Service)
		if err != nil {
			return s, err
		}
		s.LinkTarget = strings.TrimSpace(target)
		s.Installed = s.LinkTarget == layout.Binary
	}

	if s.HaveBackup, err = d.Exists(layout.Backup); err != nil {
		return s, err
	}

	if s.Installed {
		// A binary that will not execute is what a file listing hides.
		if out, err := d.Shell(layout.Binary + " --version"); err == nil {
			// Package initializers may log before Cobra handles --version. Keep only the actual
			// version line so status remains readable on both old and new builds.
			for line := range strings.SplitSeq(out, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "echod version ") {
					s.Version = line
				}
			}
			if s.Version == "" {
				s.Version = strings.TrimSpace(out)
			}
		} else {
			s.Version = "will not run: " + err.Error()
		}
	}

	if s.Name, err = ReadName(d); err != nil {
		return s, err
	}
	key, err := ReadKey(d)
	if err != nil {
		return s, err
	}
	s.Provisioned = key != ""
	s.AndroidMedia, _ = d.Exists(layout.AndroidMediaJar)
	s.PryonInstalled, _ = d.Exists(layout.PryonAPK)
	s.PryonConfigured, _ = d.Exists(layout.PryonUIDPath)
	port := strings.ToUpper(strconv.FormatInt(layout.Port, 16))
	port = strings.Repeat("0", 4-len(port)) + port
	_, code, _ := d.ShellCode("cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | grep ':" + port + " '")
	s.APIListening = code == 0

	s.ServiceState, _ = d.Getprop("init.svc." + layout.ServiceName)
	s.AgentState, _ = d.Getprop(layout.StateProp)
	s.StartedAt, _ = d.Getprop(layout.StartedProp)
	return s, nil
}
