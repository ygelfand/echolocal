package satellite

import (
	"fmt"
	"log/slog"
	"strings"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/firewall"
	"github.com/ygelfand/echolocal/internal/hardware"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/speaker"
	"github.com/ygelfand/echolocal/internal/wake"
)

// WakeSlots is how many wake word slots Home Assistant offers, and so how many assistants there are
// to wake. It is fixed on its side: the ESPHome integration adds a pipeline and a wake word select
// at index 0 and 1 for every voice-capable device, whatever the device reports.
const WakeSlots = 2

// diagnostics are the entities that make the device do something on demand, or say something about
// itself: to judge a setting by ear, to reach a pipeline without saying anything to it, or to see
// what is filling up the disk.
type diagnostics struct {
	testPlayback *esphome.Button
	wake         []*esphome.Button

	cached *esphome.Sensor
	free   *esphome.Sensor
	purge  *esphome.Button

	temperature *esphome.Sensor
	radioTemp   *esphome.Sensor
	cores       *esphome.Sensor
	coresOnline *esphome.Sensor
	load        *esphome.Sensor
	memory      *esphome.Sensor

	adb *esphome.Switch
	ip  *esphome.TextSensor
}

// The thermal zones worth showing, by the name the kernel gives them. The rest are board sensors that
// answer no question anybody asks.
const (
	cpuZone   = "mtktscpu"
	radioZone = "mtktswmt"
)

// adbPort is where adbd listens: the boot image sets service.adb.tcp.port, and this is that port.
const adbPort = 5555

func newDiagnostics(k *kit, wake func(int)) *diagnostics {
	spk := k.Speaker
	d := &diagnostics{}

	d.storage()
	d.hardware()
	d.remote()

	if spk != nil {
		// A reply is 16 kHz audio fetched over HTTP and stretched here, so judging a resampling by
		// ear against a reply confounds the filter with the network and with whatever the pipeline
		// said. This is the same path with a known signal and nothing in front of it.
		d.testPlayback = &esphome.Button{
			Base: esphome.Base{
				ObjectID: "test_playback",
				Name:     "Test playback",
				Icon:     "mdi:waveform",
				Category: esphome.CategoryDiagnostic,
			},
			OnPress: func() {
				slog.Info("test playback", "resampling", spk.Resampling(), "step", spk.Step())
				spk.PlayVoice(speaker.VoiceSweep())
			},
		}
	}

	// Not diagnostic: waking the device by hand is something to do, not something to inspect.
	for slot := range WakeSlots {
		d.wake = append(d.wake, &esphome.Button{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("wake_assistant_%d", slot+1),
				Name:     fmt.Sprintf("Wake assistant %d", slot+1),
				Icon:     "mdi:account-voice",
			},
			OnPress: func() { wake(slot) },
		})
	}
	return d
}

// remote opens the port adbd listens on, for getting at a device that is not on a cable.
//
// Deliberately not saved: the rule does not survive a reboot, so a switch that came back on would be
// claiming something that is not true. It is read from the chain instead, which also gets it right when
// echod restarts under a rule it left behind.
func (d *diagnostics) remote() {
	d.adb = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "remote_adb",
			Name:     "Remote adb",
			Icon:     "mdi:bug-outline",
			Category: esphome.CategoryDiagnostic,
		},
	}

	open, err := firewall.Opened(adbPort)
	if err != nil {
		slog.Error("reading the firewall failed", "port", adbPort, "err", err)
	}
	d.adb.Set(open)

	d.adb.OnCommand = func(on bool) {
		change, what := firewall.Close, "closing"
		if on {
			change, what = firewall.Open, "opening"
		}

		if err := change(adbPort); err != nil {
			slog.Error(what+" the adb port failed", "port", adbPort, "err", err)
			d.adb.Set(!on)
			return
		}

		slog.Warn("remote adb", "open", on, "port", adbPort)
		d.adb.Set(on)
	}

	// The protocol's device info carries the mac and no address, so this is the only place Home Assistant
	// can learn where the device actually is.
	d.ip = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "ip_address",
			Name:     "IP address",
			Icon:     "mdi:ip-network",
			Category: esphome.CategoryDiagnostic,
		},
	}
	d.address()
}

func (d *diagnostics) address() {
	var ips []string
	for _, ip := range routable() {
		ips = append(ips, ip.String())
	}
	d.ip.Set(strings.Join(ips, ", "))
}

// storage builds what the device says about its own disk. Cached is what could be deleted without
// losing anything: wake word models no slot is listening for, which Home Assistant still offers and
// will serve again on the next selection. Nothing else is cache yet.
//
// Reported in kilobytes because the protocol carries a state as a float32, which counts bytes exactly
// only to sixteen megabytes. Home Assistant converts for display, so a size class in kB can still be
// read in MB.
func (d *diagnostics) storage() {
	d.cached = &esphome.Sensor{
		Base: esphome.Base{
			ObjectID: "cached_data",
			Name:     "Cached data",
			Icon:     "mdi:cached",
			Category: esphome.CategoryDiagnostic,
		},
		Unit:        "kB",
		DeviceClass: "data_size",
		StateClass:  esphome.StateClassMeasurement,
	}
	d.free = &esphome.Sensor{
		Base: esphome.Base{
			ObjectID: "free_space",
			Name:     "Free space",
			Icon:     "mdi:harddisk",
			Category: esphome.CategoryDiagnostic,
		},
		Unit:        "kB",
		DeviceClass: "data_size",
		StateClass:  esphome.StateClassMeasurement,
	}

	d.purge = &esphome.Button{
		Base: esphome.Base{
			ObjectID: "purge_cache",
			Name:     "Purge cache",
			Icon:     "mdi:delete-sweep",
			Category: esphome.CategoryDiagnostic,
		},
		OnPress: func() {
			gone, freed := wake.Lib().Purge(inUseWakeWords())
			slog.Info("cache purged", "models", gone, "bytes", freed)
			d.measure()
		},
	}
}

// hardware builds what the board says about itself. Two temperatures rather than four: the CPU answers
// "is it working hard" and the combo chip answers "is it the radio", and the board sensors answer
// nothing anybody asks.
//
// Cores are reported twice on purpose. This kernel hotplugs them, so a device with four can be running
// two, and echod's share of a CPU means something different depending on which number you hold it
// against.
func (d *diagnostics) hardware() {
	temp := func(id, name, icon string) *esphome.Sensor {
		return &esphome.Sensor{
			Base: esphome.Base{
				ObjectID: id, Name: name, Icon: icon, Category: esphome.CategoryDiagnostic,
			},
			Unit:        "°C",
			DeviceClass: "temperature",
			StateClass:  esphome.StateClassMeasurement,
			Decimals:    1,
		}
	}
	d.temperature = temp("cpu_temperature", "CPU temperature", "mdi:thermometer")
	d.radioTemp = temp("radio_temperature", "Radio temperature", "mdi:thermometer")

	count := func(id, name string) *esphome.Sensor {
		return &esphome.Sensor{
			Base: esphome.Base{
				ObjectID: id, Name: name, Icon: "mdi:cpu-64-bit", Category: esphome.CategoryDiagnostic,
			},
			StateClass: esphome.StateClassMeasurement,
		}
	}
	d.cores = count("cpu_cores", "CPU cores")
	d.coresOnline = count("cpu_cores_online", "CPU cores online")

	d.load = &esphome.Sensor{
		Base: esphome.Base{
			ObjectID: "load_average", Name: "Load average", Icon: "mdi:gauge",
			Category: esphome.CategoryDiagnostic,
		},
		StateClass: esphome.StateClassMeasurement,
		Decimals:   2,
	}
	d.memory = &esphome.Sensor{
		Base: esphome.Base{
			ObjectID: "memory_available", Name: "Memory available", Icon: "mdi:memory",
			Category: esphome.CategoryDiagnostic,
		},
		Unit:        "kB",
		DeviceClass: "data_size",
		StateClass:  esphome.StateClassMeasurement,
	}
}

// measure republishes what the disk holds. Called at start-up and after anything that adds to the cache
// or takes from it, so the number is right the moment a purge finishes rather than a minute later.
func (d *diagnostics) measure() {
	if d.cached == nil {
		return
	}

	_, cached := wake.Cached(wake.Lib().Dir(), inUseWakeWords())
	d.cached.Set(float32(cached / 1024))

	free, err := layout.Free(layout.StateDir)
	if err != nil {
		slog.Error("reading free space failed", "dir", layout.StateDir, "err", err)
		return
	}
	d.free.Set(float32(free / 1024))
}

// Sample publishes everything that drifts on its own: the disk, and what the board says about itself.
// Called on the heartbeat, so these all come from the same moment and line up when something is wrong.
func (d *diagnostics) Sample() {
	if d == nil {
		return
	}
	d.measure()
	d.board()
	d.address()
}

// board reads the hardware and publishes what it reported. A sensor with no reading is left alone
// rather than set to zero: a board without that sensor should show unknown, not a plausible lie.
func (d *diagnostics) board() {
	if d.temperature == nil {
		return
	}
	r := hardware.Reader{}

	temps := r.Temperatures()
	set(d.temperature, reading(temps, cpuZone))
	set(d.radioTemp, reading(temps, radioZone))

	present, online := r.Cores()
	set(d.cores, present)
	set(d.coresOnline, online)

	one, _ := r.Load()
	set(d.load, one)

	available, _ := r.Memory()
	set(d.memory, available)
}

// reading picks one thermal zone out of what was found.
func reading(temps map[string]float64, zone string) hardware.Reading {
	v, ok := temps[zone]
	return hardware.Reading{Value: v, Known: ok}
}

func set(s *esphome.Sensor, r hardware.Reading) {
	if s != nil && r.Known {
		s.Set(float32(r.Value))
	}
}

// inUseWakeWords is what the slots are set to, which is what a purge has to leave alone.
func inUseWakeWords() []string {
	saved := settings.Get().Wake

	var ids []string
	for slot := range WakeSlots {
		if id := saved.WordID(slot); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func (d *diagnostics) entities() []esphome.Entity {
	ents := []esphome.Entity{
		d.cached, d.free, d.purge,
		d.temperature, d.radioTemp, d.cores, d.coresOnline, d.load, d.memory,
		d.adb, d.ip,
	}
	if d.testPlayback != nil {
		ents = append(ents, d.testPlayback)
	}
	for _, b := range d.wake {
		ents = append(ents, b)
	}
	return ents
}
