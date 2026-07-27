package satellite

import (
	"context"
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/input"
)

// evdev key codes for the four buttons on top of the device.
const (
	keyMute       = 113
	keyVolumeDown = 114
	keyVolumeUp   = 115
	keyAction     = 138
)

// LongPress is how long a button must be held to count as held rather than pressed, and
// RepeatInterval how fast a repeating button ramps while down.
const (
	LongPress      = 700 * time.Millisecond
	RepeatInterval = 200 * time.Millisecond
)

// Event types, as Home Assistant sees them.
const (
	EventPress = "press"
	EventHold  = "hold"
)

// button is what a press means to us.
type button struct {
	name string

	// repeats says whether holding should keep firing the action. Volume ramps; mute does not.
	repeats bool

	// press runs on a short press, hold on a long one. Either may be nil, and a button with
	// neither still reports its presses to Home Assistant.
	press func()
	hold  func()

	event *esphome.Event
}

// buttons builds the set, with an event entity each so Home Assistant can automate on presses
// even where the device does nothing itself — the action button, until there is a voice turn to
// start.
func newButtons(player *mediaPlayer, mute *muteSwitch) map[uint16]*button {
	spk := player.speaker

	out := map[uint16]*button{
		keyVolumeUp:   {name: "volume_up", repeats: true, press: func() { player.adjust(1) }},
		keyVolumeDown: {name: "volume_down", repeats: true, press: func() { player.adjust(-1) }},
		keyAction: {
			name:  "action",
			press: func() { chime(spk, toneAction) },
			hold:  func() { chime(spk, toneActionHold) },
		},
	}
	if mute != nil {
		out[keyMute] = &button{
			name:  "mute",
			press: mute.toggle,
			hold:  func() { chime(spk, toneMuteHold) },
		}
	}

	for _, b := range out {
		b.event = &esphome.Event{
			Base:  esphome.Base{ObjectID: "button_" + b.name, Name: title(b.name) + " Button"},
			Types: []string{EventPress, EventHold},
		}
	}
	return out
}

func title(name string) string {
	out := []rune(name)
	upper := true
	for i, r := range out {
		switch {
		case r == '_':
			out[i] = ' '
			upper = true
		case upper:
			out[i] = r - 32
			upper = false
		}
	}
	return string(out)
}

// watchButtons reads the input nodes once and dispatches to handlers, rather than each consumer
// opening the same devices.
//
// A press is reported on release so its length is known, unless the button is held past
// LongPress, when the hold fires immediately and the release is swallowed — waiting for release
// to act on a hold feels broken.
func watchButtons(ctx context.Context, buttons map[uint16]*button) {
	devices, err := input.List()
	if err != nil {
		slog.Error("listing input devices failed", "err", err)
		return
	}
	if len(devices) == 0 {
		slog.Warn("no input devices; buttons will not work")
		return
	}

	for _, d := range devices {
		go func(d *input.Device) {
			defer d.Close()
			watch(ctx, d, buttons)
		}(d)
	}
}

// press tracks one button between its press and release. This keypad emits no autorepeat, only
// press and release, so holding is timed here rather than counted from repeat events.
type press struct {
	mu     sync.Mutex
	held   bool
	hold   *time.Timer
	repeat *time.Ticker
	done   chan struct{}
}

func (p *press) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.hold != nil {
		p.hold.Stop()
	}
	if p.done != nil {
		close(p.done)
		p.done = nil
	}
	if p.repeat != nil {
		p.repeat.Stop()
	}
}

func watch(ctx context.Context, d *input.Device, buttons map[uint16]*button) {
	state := make(map[uint16]*press)

	for {
		e, err := d.Read()
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("reading input failed", "device", d.Path, "err", err)
			}
			return
		}
		if e.Type != input.EvKey {
			continue
		}
		b, ok := buttons[e.Code]
		if !ok {
			continue
		}

		switch e.Value {
		case 1:
			p := &press{}
			state[e.Code] = p

			// A repeating button acts at once and then ramps, so a tap moves one step.
			if b.repeats && b.press != nil {
				b.press()
				p.done = make(chan struct{})
				p.repeat = time.NewTicker(RepeatInterval)
				go func(done <-chan struct{}, t *time.Ticker) {
					for {
						select {
						case <-done:
							return
						case <-t.C:
							b.press()
						}
					}
				}(p.done, p.repeat)
			}

			p.hold = time.AfterFunc(LongPress, func() {
				p.mu.Lock()
				p.held = true
				p.mu.Unlock()

				// A repeating button keeps ramping until it is let go; stopping here ended the
				// ramp after three ticks.
				if !b.repeats {
					p.stop()
				}

				slog.Info("button held", "button", b.name)
				b.event.Trigger(EventHold)
				if b.hold != nil {
					b.hold()
				}
			})

		case 0:
			p, ok := state[e.Code]
			if !ok {
				continue
			}
			delete(state, e.Code)
			p.stop()

			p.mu.Lock()
			held := p.held
			p.mu.Unlock()
			if held {
				continue
			}

			slog.Info("button pressed", "button", b.name)
			b.event.Trigger(EventPress)
			if b.press != nil && !b.repeats {
				b.press()
			}
		}
	}
}
