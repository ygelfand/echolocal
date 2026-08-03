// Package voice is a turn: hearing a wake word, listening, waiting for an answer and playing it.
//
// Two halves. The conversation is a state machine on its own goroutine, so a button press, a pipeline
// event and a timeout cannot race each other. Around it sits the voice satellite, which is what Home
// Assistant talks to.
//
// Nothing here has an entity: a turn is not a setting and not a reading.
package voice

import (
	"context"
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"
	"google.golang.org/protobuf/proto"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/wakeword"
	"github.com/ygelfand/echolocal/internal/hardware/buttons"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/lib/wake"
)

func init() {
	component.Register(component.Device, Get(), component.Order(30))
}

// Features is what the device claims it can do with a voice pipeline. Announce and
// StartConversation go together and both need a media player, which this device has.
const Features = esphome.DefaultVoiceFeatures |
	esphome.FeatureSpeaker |
	esphome.FeatureAnnounce |
	esphome.FeatureStartConversation

type Voice struct {
	vs   *esphome.VoiceSatellite
	turn *conversation
}

var (
	once   sync.Once
	shared *Voice
)

func Get() *Voice {
	once.Do(func() { shared = build() })
	return shared
}

// build makes the satellite and the conversation together: the satellite's callbacks are the
// conversation's inputs, and the conversation answers back through it.
//
// What the device can hear is not stored — it is worked out on every configuration request, because
// models arrive and are deleted while the device runs.
func build() *Voice {
	ours := wake.Lib().Ours()
	active := activeWakeWords(ours, wakeword.Slots)

	v := &Voice{
		vs: &esphome.VoiceSatellite{
			ActiveWakeWords:     active,
			MaxActiveWakeWords:  wakeword.Slots,
			OnExternalWakeWords: wakeword.Answer,
		},
	}
	v.turn = newConversation(v.vs)
	slog.Info("wake words", "ours", len(ours), "active", active)

	wakeword.Requested.Listen(v.Start)

	// The action button is the only one where a hold means something different from a press, and what
	// it means is the conversation's to decide. The other buttons are somebody else's listeners.
	buttons.Get().Events.Listen(func(e buttons.Event) {
		if e.Name != buttons.Action {
			return
		}
		switch e.Kind {
		case buttons.Tap:
			v.Action()
		case buttons.Hold:
			v.ActionHold()
		}
	})
	return v
}

func (v *Voice) Name() string { return "conversation" }

// Handle is the satellite's own protocol messages, which have no entity to arrive through.
func (v *Voice) Handle(ctx context.Context, c *esphome.Conn, msg proto.Message) error {
	return v.vs.Handle(ctx, c, msg)
}

// Run owns the conversation until ctx is cancelled. Nothing happens on a wake word until it is
// running.
func (v *Voice) Run(ctx context.Context) error {
	v.turn.Run(ctx)
	return nil
}

// Ready reports whether Home Assistant has a voice pipeline listening. Wake detection runs before
// that happens, but nothing can be done with a detection until it does, so this is what the device
// shows on the ring while it comes up.
func (v *Voice) Ready() bool { return v.vs.Subscribed() }

// Start asks for a turn as if that slot's wake word had fired, which is how detection and the
// buttons both reach a pipeline. What that means from the phase the conversation is already in is
// the conversation's decision, not the caller's.
func (v *Voice) Start(slot int) { v.turn.Start(slot) }

// Action is the action button: it gives up on whatever is happening, or starts something if nothing
// is. Cancelling is the more useful half — it is the way out of a turn that is waiting on a pipeline
// that is not going to answer.
func (v *Voice) Action() {
	// Anything audible is what the press meant. Asking a question is what the button is for when the
	// device is doing nothing; while it is talking or playing, reaching for it means make it stop.
	if v.Stop() {
		return
	}

	// No wake word, so no slot to pair with: the first pipeline is the one Home Assistant falls back
	// to for anything that reports no phrase.
	v.turn.Start(0)
}

// Interrupt is the stop word.
//
// It only acts while the device is making a sound. "Stop" is an ordinary word: somebody halfway through
// "stop the timer" is talking to Home Assistant, not to the device, and cutting their turn off there
// would be worse than not listening for it at all. Nothing is playing then, so there is nothing the word
// could sensibly mean.
func (v *Voice) Interrupt() {
	if !speaker.Sound().Busy() {
		if playing, _ := media.Get().Playing(); !playing {
			slog.Debug("stop word ignored, nothing to stop")
			return
		}
	}
	v.Stop()
}

// Stop ends whatever the device is doing audibly, and reports whether there was anything to end.
//
// One ladder, because there is one meaning: a turn is cancelled, a sound is silenced, a track is
// stopped. The action button falls through to starting a turn when it returns false; a stop word has
// nothing to fall through to and simply does nothing.
func (v *Voice) Stop() bool {
	if v.turn.Busy() {
		v.turn.Cancel()
		return true
	}

	// An announcement outside a turn: nothing is listening, but something is playing.
	if sound := speaker.Sound(); sound.Busy() {
		sound.Silence()
		return true
	}

	if playing, _ := media.Get().Playing(); playing {
		media.Get().Pause()
		return true
	}
	return false
}

// ActionHold is holding the action button, which reaches the second assistant. Holding does not
// cancel: a press is the way out of a turn, so holding while one is running interrupts it with the
// other assistant instead, which is the same thing saying the other wake word would do.
func (v *Voice) ActionHold() { v.turn.Start(1) }

// OnWakeWord is called when Home Assistant changes the selection, so the engine can follow. It is
// given every slot: load reports which of them it accepted, and only those are echoed back as
// active, because Home Assistant takes the echo as authoritative and reverts a slot whose word is
// missing from it. A slot the device will not run therefore reverts in the interface rather than
// sitting there looking armed.
//
// selected is called after the slots have been written, for whatever else a new selection changes.
func (v *Voice) OnWakeWord(load func(ids []string) []string, selected func()) {
	v.vs.OnSetActiveWakeWords = func(ids []string) {
		accepted := load(ids)
		v.vs.ActiveWakeWords = accepted

		for slot := range wakeword.Slots {
			id := ""
			if slot < len(accepted) {
				id = accepted[slot]
			}
			if err := config.Set().Wake(slot).ID(id); err != nil {
				slog.Error("saving the wake word failed", "slot", slot+1, "err", err)
			}
		}
		if len(accepted) != len(ids) {
			slog.Warn("some wake words were refused", "asked", ids, "running", accepted)
		}
		selected()
	}
}

// ActiveWakeWords is what the device is advertising as listening, by slot.
func (v *Voice) ActiveWakeWords() []string { return v.vs.ActiveWakeWords }

// SetActiveWakeWords corrects what is advertised to what is actually running. The engine loads at
// start-up rather than waiting to be told, so this is how the advertisement is reconciled with what
// came up: anything that failed to load is not claimed.
func (v *Voice) SetActiveWakeWords(ids []string) {
	v.vs.ActiveWakeWords = ids
	slog.Info("wake words listening", "active", ids)
}
