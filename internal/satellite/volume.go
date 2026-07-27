package satellite

import (
	"log/slog"
	"math"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/led"
)

// Volume runs 0..30, the range Android gives STREAM_MUSIC and the one Amazon's firmware used,
// so a step here is a step there.
const VolumeSteps = 30

// volumeFlash is how long the ring shows the level after a change.
const volumeFlash = 2 * time.Second

// volumeControl is the speaker level, settable from Home Assistant and from the buttons.
//
// Nothing plays audio yet, so the value is state only: it is applied when the playback path
// exists, and the buttons and ring already behave as they will then.
type volumeControl struct {
	num  *esphome.Number
	ring *ringLight
	log  *slog.Logger
}

func newVolumeControl(ring *ringLight, log *slog.Logger) *volumeControl {
	v := &volumeControl{
		num: &esphome.Number{
			Base: esphome.Base{ObjectID: "volume", Name: "Volume", Icon: "mdi:volume-high"},
			Min:  0, Max: VolumeSteps, Step: 1,
		},
		ring: ring,
		log:  log,
	}
	v.num.OnCommand = v.set
	v.num.Set(VolumeSteps / 2)
	return v
}

func (v *volumeControl) set(step float32) {
	step = float32(math.Round(float64(step)))
	step = float32(math.Max(0, math.Min(VolumeSteps, float64(step))))

	v.num.Set(step)
	v.show(step)
	v.log.Info("volume", "step", step, "of", VolumeSteps)
}

func (v *volumeControl) adjust(delta float32) { v.set(v.num.Get() + delta) }

// show lights the level as a clockwise arc, the leading segment dimmed by the fraction of a
// segment the level does not fill.
func (v *volumeControl) show(step float32) {
	frame := led.Arc(float64(step)/VolumeSteps, led.Color{R: 0xFF, G: 0xFF, B: 0xFF})
	v.ring.Flash(frame, volumeFlash)
}
