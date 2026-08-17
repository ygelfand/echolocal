// Package diag is what the device says about itself: its temperature, its cores, its disk, its link,
// and where on the network it is. Nothing here changes what it does.
package diag

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/android/firewall"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/bluetooth"
	"github.com/ygelfand/echolocal/internal/feature/wakeword"
	"github.com/ygelfand/echolocal/internal/hardware/metrics"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/wake"
)

func init() {
	component.Register(component.Network, Get(), component.Order(90))
}

// The thermal zones worth showing, by the name the kernel gives them. The rest are board sensors that
// answer no question anybody asks.
const (
	cpuZone   = "mtktscpu"
	radioZone = "mtktswmt"
)

// adbPort is where adbd listens: the boot image sets service.adb.tcp.port, and this is that port.
const adbPort = 5555

type Diag struct {
	testPlayback *esphome.Button

	cached *esphome.Sensor
	free   *esphome.Sensor
	purge  *esphome.Button

	temperature *esphome.Sensor
	radioTemp   *esphome.Sensor
	cores       *esphome.Sensor
	coresOnline *esphome.Sensor
	load        *esphome.Sensor
	memory      *esphome.Sensor
	lux         *esphome.Sensor
	roomLevel   *esphome.Sensor
	roomFloor   *esphome.Sensor

	luxPath string

	adb   *esphome.Switch
	ip    *esphome.TextSensor
	color *esphome.TextSensor

	signal *esphome.Sensor
	rxRate *esphome.Sensor
	txRate *esphome.Sensor
	ads    *esphome.Sensor

	interval *esphome.Number

	// wake restarts the collector's wait when the interval changes, so a shorter one takes effect now
	// rather than after the wait already running. Buffered: a change while nothing is waiting is not
	// worth blocking on.
	wake chan struct{}

	last struct {
		at       time.Time
		rx, tx   float64
		reports  uint64
		recorded bool
	}
}

var (
	once   sync.Once
	shared *Diag
)

func Get() *Diag {
	once.Do(func() {
		shared = &Diag{wake: make(chan struct{}, 1)}
		shared.storage()
		shared.hardware()
		shared.radio()
		shared.remote()
		shared.playback()
		shared.identity()
		shared.collector()
	})
	return shared
}

func (d *Diag) Name() string { return "diagnostics" }

func (d *Diag) Entities() []esphome.Entity {
	return []esphome.Entity{
		d.cached, d.free, d.purge,
		d.temperature, d.radioTemp, d.cores, d.coresOnline, d.load, d.memory, d.lux,
		d.roomLevel, d.roomFloor,
		d.adb, d.ip, d.color, d.signal, d.rxRate, d.txRate, d.ads,
		d.testPlayback, d.interval,
	}
}

// collector builds the one setting these readings have: how often to take them.
func (d *Diag) collector() {
	d.interval = &esphome.Number{
		Base: esphome.Base{
			ObjectID: "metrics_interval",
			Name:     "Metrics interval",
			Icon:     "mdi:timer-sync",
			Category: esphome.CategoryConfig,
		},
		Min: 10, Max: 3600, Step: 10, Unit: "s",
		Mode: esphome.NumberBox,
	}

	d.interval.OnCommand = func(v float32) {
		d.interval.Set(v)
		if err := config.Set().Diag().Interval(int(v)); err != nil {
			slog.Error("saving the metrics interval failed", "err", err)
		}

		// The wait already running was measured against the old interval.
		select {
		case d.wake <- struct{}{}:
		default:
		}
	}
}

// Run collects the readings that drift, once at the start and then on the interval.
//
// Immediately, not after the first wait: the readings go into entities that hold their last value
// until Home Assistant asks, so a device that waited would report nothing at all until then — and a
// restart is exactly when somebody is looking.
func (d *Diag) Run(ctx context.Context) error {
	for {
		d.Sample()

		t := time.NewTimer(time.Duration(config.Get().Diag.Interval) * time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-d.wake:
			t.Stop()
		case <-t.C:
		}
	}
}

// playback is a known signal through the reply path with nothing in front of it. A reply is 16 kHz
// audio fetched over HTTP and stretched here, so judging a resampling by ear against a reply
// confounds the filter with the network and with whatever the pipeline said.
func (d *Diag) playback() {
	spk := speaker.Get()

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

// remote opens the port adbd listens on, for getting at a device that is not on a cable.
func (d *Diag) remote() {
	d.adb = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "remote_adb",
			Name:     "Remote adb",
			Icon:     "mdi:bug-outline",
			Category: esphome.CategoryDiagnostic,
		},
	}

	d.adb.OnCommand = func(on bool) {
		if err := d.reachable(on); err != nil {
			d.adb.Set(!on)
			return
		}
		if err := config.Set().Diag().RemoteADB(on); err != nil {
			slog.Error("saving a setting failed", "setting", d.adb.ObjectID, "err", err)
		}
	}

	// The protocol's device info carries the mac and no address, so this is the only place Home
	// Assistant can learn where the device actually is.
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

// reachable opens or closes the adb port and moves the switch to match what the chain now says.
func (d *Diag) reachable(on bool) error {
	change, what := func() error { return firewall.Close(firewall.ADB) }, "closing"
	if on {
		change, what = func() error { return firewall.Open(firewall.ADB, adbPort) }, "opening"
	}

	if err := change(); err != nil {
		slog.Error(what+" the adb port failed", "port", adbPort, "err", err)
		return err
	}

	slog.Warn("remote adb", "open", on, "port", adbPort)
	d.adb.Set(on)
	return nil
}

func (d *Diag) address() {
	var ips []string
	for _, ip := range metrics.Addresses() {
		ips = append(ips, ip.String())
	}
	d.ip.Set(strings.Join(ips, ", "))
}

// identity is what the device is rather than what it is doing. The shell colour comes off the factory
// idme partition, which never changes, so it is read once here rather than sampled.
func (d *Diag) identity() {
	d.color = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "hardware_color",
			Name:     "Hardware color",
			Icon:     "mdi:palette",
			Category: esphome.CategoryDiagnostic,
		},
	}
	d.color.Set(layout.Color(layout.Idme("productid2"), layout.Idme("serial")))
}

// storage builds what the device says about its own disk. Cached is what could be deleted without
// losing anything: wake word models no slot is listening for, which Home Assistant still offers and
// will serve again on the next selection. Nothing else is cache yet.
//
// Reported in kilobytes because the protocol carries a state as a float32, which counts bytes exactly
// only to sixteen megabytes. Home Assistant converts for display, so a size class in kB can still be
// read in MB.
func (d *Diag) storage() {
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
			gone, freed := wake.Lib().Purge(inUse())
			slog.Info("cache purged", "models", gone, "bytes", freed)
			d.Measure()
		},
	}
}

// radio builds what the wireless side reports: the link, what it carries, and what the Bluetooth
// proxy hears. They share one antenna, so these belong together — scanning is paid for in throughput.
func (d *Diag) radio() {
	d.signal = &esphome.Sensor{
		Base: esphome.Base{
			ObjectID: "wifi_signal", Name: "Wifi signal", Icon: "mdi:wifi",
			Category: esphome.CategoryDiagnostic,
		},
		Unit:        "dBm",
		DeviceClass: "signal_strength",
		StateClass:  esphome.StateClassMeasurement,
	}

	rate := func(id, name, icon string) *esphome.Sensor {
		return &esphome.Sensor{
			Base: esphome.Base{
				ObjectID: id, Name: name, Icon: icon, Category: esphome.CategoryDiagnostic,
			},
			Unit:       "kB/s",
			StateClass: esphome.StateClassMeasurement,
			Decimals:   1,
		}
	}
	d.rxRate = rate("wifi_received", "Wifi received", "mdi:download-network")
	d.txRate = rate("wifi_sent", "Wifi sent", "mdi:upload-network")

	d.ads = &esphome.Sensor{
		Base: esphome.Base{
			ObjectID: "ble_advertisements", Name: "BLE advertisements", Icon: "mdi:bluetooth-audio",
			Category: esphome.CategoryDiagnostic,
		},
		Unit:       "/s",
		StateClass: esphome.StateClassMeasurement,
		Decimals:   1,
	}
}

// hardware builds what the board says about itself. Two temperatures rather than four: the CPU answers
// "is it working hard" and the combo chip answers "is it the radio", and the board sensors answer
// nothing anybody asks.
//
// Cores are reported twice on purpose. This kernel hotplugs them, so a device with four can be running
// two, and echod's share of a CPU means something different depending on which number you hold it
// against.
func (d *Diag) hardware() {
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

	// Sampled with everything else, so at the default interval this shows the room's resting level rather
	// than tracking speech. That is the number worth having: the sensitivity is set as a distance above
	// the floor, and both sides of it are here.
	room := func(id, name, icon string) *esphome.Sensor {
		return &esphome.Sensor{
			Base: esphome.Base{
				ObjectID: id, Name: name, Icon: icon,
				DeviceID: component.DeviceMicrophone,
				Category: esphome.CategoryDiagnostic,
			},
			StateClass: esphome.StateClassMeasurement,
			Decimals:   2,
		}
	}
	d.roomLevel = room("room_level", "Room level", "mdi:waveform")
	d.roomFloor = room("room_floor", "Room floor", "mdi:arrow-collapse-down")

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

	d.luxPath = metrics.Reader{}.LuxPath()
	if d.luxPath == "" {
		slog.Warn("no light sensor found")
	} else {
		slog.Info("light sensor", "at", d.luxPath)
	}
	d.lux = &esphome.Sensor{
		Base: esphome.Base{
			ObjectID: "lux", Name: "Lux", Icon: "mdi:brightness-6",
			Category: esphome.CategoryDiagnostic,
		},
		Unit:        "lx",
		DeviceClass: "illuminance",
		StateClass:  esphome.StateClassMeasurement,
	}
}

// Measure republishes what the disk holds. Called at start-up and after anything that adds to the cache
// or takes from it, so the number is right the moment a purge finishes rather than a minute later.
func (d *Diag) Measure() {
	_, cached := wake.Cached(wake.Lib().Dir(), inUse())
	d.cached.Set(float32(cached / 1024))

	free, err := metrics.Free(layout.StateDir)
	if err != nil {
		slog.Error("reading free space failed", "dir", layout.StateDir, "err", err)
		return
	}
	d.free.Set(float32(free / 1024))
}

// Restore is the disk as it stands at start-up, which the slots decide: a model no slot wants is
// cache. It runs with the rest so the numbers are there before Home Assistant asks.
func (d *Diag) Restore(c config.Config) {
	d.Measure()

	d.interval.Set(float32(c.Diag.Interval))
	slog.Info("restored", "what", d.interval.ObjectID, "using", c.Diag.Interval)

	// The chain is empty after a reboot but not after an echod restart, so the rule is put back or
	// taken away rather than either being assumed.
	if err := d.reachable(c.Diag.RemoteADB); err == nil {
		slog.Info("restored", "what", d.adb.ObjectID, "using", c.Diag.RemoteADB)
	}
}

// Sample takes every reading at once, so they come from the same moment and line up when something
// is wrong.
func (d *Diag) Sample() {
	d.Measure()
	d.board()
	d.address()
	d.wireless()
	d.room()
}

// room is what the level the ring can react to is reading, and the floor it is measured against.
func (d *Diag) room() {
	source := mic.Get()
	d.roomLevel.Set(float32(source.Peak()))
	d.roomFloor.Set(float32(source.Floor()))
}

// wireless publishes the link and the two things competing for it. Rates come from what changed since
// the last beat, so the first one after a start reports nothing rather than counting from boot.
func (d *Diag) wireless() {
	signal, rx, tx := metrics.Reader{}.Wifi()
	set(d.signal, signal)

	reports := bluetooth.Get().Reports()

	now := time.Now()
	was := d.last
	d.last.at, d.last.rx, d.last.tx, d.last.reports, d.last.recorded = now, rx.Value, tx.Value, reports, true

	if !was.recorded {
		return
	}
	secs := now.Sub(was.at).Seconds()
	if secs <= 0 {
		return
	}

	if rx.Known {
		d.rxRate.Set(float32((rx.Value - was.rx) / secs / 1024))
	}
	if tx.Known {
		d.txRate.Set(float32((tx.Value - was.tx) / secs / 1024))
	}
	d.ads.Set(float32(float64(reports-was.reports) / secs))
}

// board reads the hardware and publishes what it reported. A sensor with no reading is left alone
// rather than set to zero: a board without that sensor should show unknown, not a plausible lie.
func (d *Diag) board() {
	r := metrics.Reader{}

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

	set(d.lux, r.Lux(d.luxPath))
}

// reading picks one thermal zone out of what was found.
func reading(temps map[string]float64, zone string) metrics.Reading {
	v, ok := temps[zone]
	return metrics.Reading{Value: v, Known: ok}
}

func set(s *esphome.Sensor, r metrics.Reading) {
	if r.Known {
		s.Set(float32(r.Value))
	}
}

// inUse is what the slots are set to, which is what a purge has to leave alone.
func inUse() []string {
	saved := config.Get().Wake

	var ids []string
	for slot := range wakeword.Slots {
		if id := saved.Slot(slot).ID; id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
