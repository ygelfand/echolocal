package satellite

import (
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/gpio"
	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/speaker"
)

// kit is the hardware a satellite was handed and the pieces built on top of it.
//
// Any of the hardware may be nil, because acquiring it can fail and the device runs degraded rather
// than not at all. The assembled fields are filled in construction order by New, so a piece may only
// read what was set before it.
type kit struct {
	Speaker *speaker.Player

	// Sound is who may make one, so that stopping is one call rather than one per source.
	Sound   *speaker.Driver
	Mic     *mic.Source
	Mute    *gpio.Mute
	MuteLED *gpio.MuteLED
	LEDs    *led.Driver

	Ring   *ringLight
	Player *mediaPlayer
	Wake   *wakeControl
	Log    *activity
	Voice  *esphome.VoiceSatellite
}
