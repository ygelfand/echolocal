// Package feedback is how the device tells the person in front of it that something happened.
//
// The occasions are named here — a failure, a request dropped — and each decides for itself what it
// uses: both outputs, a tone alone, or the ring alone. What matters is that the decision is made in
// one place, so a caller says what happened rather than choosing how to say it, and two callers
// reporting the same thing cannot disagree about whether it makes a sound.
//
// Only momentary things live here. An indication that lasts as long as a state — the ring while the
// microphones are cut, the ring following the room — belongs to whatever owns that state, because
// ending it is that owner's business.
package feedback

import (
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

func init() {
	component.Register(component.Device, Get(), component.Order(15))
}

// failureFlash is how long the ring shows a failure, and failureColor what it shows. Red is not used
// anywhere else momentary, so it never has to be told apart from an effect or a volume arc.
const failureFlash = 1500 * time.Millisecond

var failureColor = led.Color{R: 0xC0, G: 0x00, B: 0x00}

// Feedback owns the settings for what these occasions look like. What they sound like is not a
// setting: the tones are short and few, and telling them apart is the point of them.
type Feedback struct {
	failure *esphome.Select
}

var (
	once   sync.Once
	shared *Feedback
)

func Get() *Feedback {
	once.Do(func() {
		shared = &Feedback{
			failure: &esphome.Select{
				Base: esphome.Base{
					ObjectID: "failure_effect",
					DeviceID: component.DeviceRing,
					Name:     "Ring on failure",
					Icon:     "mdi:alert-circle",
					Category: esphome.CategoryConfig,
				},
			},
		}
		component.BindEffect(shared.failure, led.EffectNames(), nil, config.Set().Ring().Trouble)
	})
	return shared
}

func (f *Feedback) Name() string { return "feedback" }

func (f *Feedback) Entities() []esphome.Entity { return []esphome.Entity{f.failure} }

func (f *Feedback) Restore(c config.Config) {
	component.RestoreEffect(f.failure, c.Ring.Trouble, nil, config.Set().Ring().Trouble)
}

// Failure says something did not work: a request that cannot be served has to sound and look
// different from one that was, or it is indistinguishable from the device having ignored the person.
//
// The ring runs on its own claim above whatever is happening, so ending the thing that failed cannot
// take the indication away with it. Which animation is the user's choice, and None is one of the
// answers: some rooms would rather the ring stayed out of it.
func Failure() {
	speaker.Sound().Chime(speaker.ToneTrouble)

	name := config.Get().Ring.Trouble
	if name == "" {
		return
	}
	led.Get().Claim(led.PriorityTrouble).ShowFor(
		led.Content{Effect: name, Base: failureColor}, failureFlash)
}

// Cancelled is a request dropped on purpose, which is neither a failure nor an answer. It only
// sounds: the ring is already showing the turn ending.
func Cancelled() { speaker.Sound().Chime(speaker.ToneCancel) }
