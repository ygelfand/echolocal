// Package timer is a kitchen timer: Home Assistant keeps the timers, the device counts them down on
// the ring and rings when one finishes.
//
// Home Assistant sends an event when a timer starts, is changed, is cancelled or finishes, and
// nothing in between — so the countdown here is local arithmetic against a monotonic clock, corrected
// whenever an event arrives. A finished timer is dropped at that end, so the ringing is entirely this
// device's to start and to stop.
package timer

import (
	"cmp"
	"context"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/lib/safe"
)

func init() {
	component.Register(component.Device, Get(), component.Order(31))
}

const (
	// refresh is how often the countdown is redrawn. The frame is only sent when it changes, so a long
	// timer costs nothing between segments.
	refresh = 250 * time.Millisecond

	// ringFor is how long a finished timer rings for if nobody stops it, and ringEvery how often the
	// tone repeats within that.
	ringFor   = 15 * time.Minute
	ringEvery = 2 * time.Second

	// alarmLevel is louder than the tones the device uses for feedback: this one is meant to fetch
	// somebody from another room.
	alarmLevel = 0.6
)

var (
	countdownColor = led.Color{R: 0xFF, G: 0x8C, B: 0x00}
	alarmColor     = led.Color{R: 0xFF, G: 0x40, B: 0x00}
)

// Timers is every timer Home Assistant has told this device about.
type Timers struct {
	countdown *led.Claim
	alarm     *led.Claim

	// names is what is counting down, since the ring can only ever say that something is.
	names *esphome.TextSensor

	// woke is how a new timer restarts the redraw, which stops while there is nothing counting down.
	woke chan struct{}

	mu    sync.Mutex
	held  map[string]*timer
	stop  context.CancelFunc
	shown []led.Color
}

// timer is one of them. left is what was last known and at is when that was true, so a running timer
// is left minus however long ago that was.
type timer struct {
	name   string
	total  time.Duration
	left   time.Duration
	at     time.Time
	active bool
}

func (t *timer) remaining(now time.Time) time.Duration {
	if !t.active {
		return t.left
	}
	return max(t.left-now.Sub(t.at), 0)
}

var (
	once   sync.Once
	shared *Timers
)

func Get() *Timers {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Timers {
	return &Timers{
		countdown: led.Get().Claim(led.PriorityTimer),
		alarm:     led.Get().Claim(led.PriorityAlarm),
		names: &esphome.TextSensor{
			Base: esphome.Base{
				ObjectID: "timers",
				Name:     "Timers",
				Icon:     "mdi:timer-outline",
				Category: esphome.CategoryDiagnostic,
			},
		},
		woke: make(chan struct{}, 1),
		held: map[string]*timer{},
	}
}

func (t *Timers) Name() string { return "timers" }

func (t *Timers) Entities() []esphome.Entity { return []esphome.Entity{t.names} }

// Run redraws the countdown while there is one, and waits to be woken while there is not.
func (t *Timers) Run(ctx context.Context) error {
	for {
		if !t.counting() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.woke:
			}
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(refresh):
		}
		t.show()
	}
}

func (t *Timers) counting() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.soonest(time.Now()) != nil
}

// Event is a timer event from Home Assistant.
func (t *Timers) Event(e esphome.TimerEvent) {
	slog.Info("timer",
		"event", e.Type, "name", e.Name, "left", e.SecondsLeft, "total", e.TotalSeconds, "active", e.IsActive)

	switch e.Type {
	case api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_STARTED,
		api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_UPDATED:
		t.set(e)
	case api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_CANCELLED:
		t.forget(e.TimerID)
	case api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_FINISHED:
		t.finished(e)
	}
	t.show()
	t.publish()
}

// publish names what is counting down, soonest first. It follows the table rather than the clock, so
// it does not send Home Assistant anything four times a second.
func (t *Timers) publish() {
	t.mu.Lock()
	now := time.Now()
	running := make([]*timer, 0, len(t.held))
	for _, c := range t.held {
		if c.active {
			running = append(running, c)
		}
	}
	t.mu.Unlock()

	slices.SortFunc(running, func(a, b *timer) int { return cmp.Compare(a.remaining(now), b.remaining(now)) })

	names := make([]string, 0, len(running))
	for _, c := range running {
		names = append(names, cmp.Or(c.name, "Timer"))
	}
	t.names.Set(strings.Join(names, ", "))
}

// Forget drops every timer, for a Home Assistant that has stopped listening: it holds them in memory
// and comes back without them, so a countdown that outlived the connection is counting down to
// nothing. Whatever is already ringing carries on, since that no longer depends on Home Assistant.
func (t *Timers) Forget() {
	t.mu.Lock()
	n := len(t.held)
	t.held = map[string]*timer{}
	t.mu.Unlock()

	if n > 0 {
		slog.Info("timers forgotten", "count", n)
	}
	t.show()
	t.publish()
}

// Ringing reports whether a finished timer is sounding, which is one of the things that makes the
// device audible.
func (t *Timers) Ringing() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stop != nil
}

// Stop silences a ringing timer and reports whether there was one. The timer itself is Home
// Assistant's, and it has already dropped it.
func (t *Timers) Stop() bool {
	t.mu.Lock()
	stop := t.stop
	t.mu.Unlock()

	if stop == nil {
		return false
	}
	stop()
	return true
}

func (t *Timers) set(e esphome.TimerEvent) {
	t.mu.Lock()
	t.held[e.TimerID] = &timer{
		name:   e.Name,
		total:  time.Duration(e.TotalSeconds) * time.Second,
		left:   time.Duration(e.SecondsLeft) * time.Second,
		at:     time.Now(),
		active: e.IsActive,
	}
	t.mu.Unlock()

	select {
	case t.woke <- struct{}{}:
	default:
	}
}

func (t *Timers) forget(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.held, id)
}

// finished starts the ringing, or joins a timer that is already ringing: one alarm covers however
// many of them went off.
func (t *Timers) finished(e esphome.TimerEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.held, e.TimerID)
	if t.stop != nil {
		return
	}

	var ctx context.Context
	ctx, t.stop = context.WithCancel(context.Background())
	safe.Go("timer alarm", func() { t.ring(ctx) })
}

// ring sounds until it is stopped or ringFor is up. Music is ducked rather than suspended: it is one
// room of what may be a whole house, and the tone is audible over it.
func (t *Timers) ring(ctx context.Context) {
	defer func() {
		t.mu.Lock()
		t.stop = nil
		t.mu.Unlock()
		t.show()
	}()

	t.alarm.Play(led.EffectPulse, alarmColor)
	defer t.alarm.Clear()

	sound := speaker.Sound()
	sound.Backgrounds().Duck(true)
	defer sound.Backgrounds().Duck(false)

	over := time.After(ringFor)
	for {
		sound.Interject(func(p *speaker.Player) { p.Chime(alarmLevel, speaker.ToneTimer...) })

		select {
		case <-ctx.Done():
			return
		case <-over:
			slog.Info("timer rang out", "for", ringFor)
			return
		case <-time.After(ringEvery):
		}
	}
}

// show draws the soonest timer, or clears the ring when there is none. It sends nothing when the
// frame has not moved, so the driver is not woken four times a second for a timer with an hour to go.
func (t *Timers) show() {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	var next []led.Color
	if soon := t.soonest(now); soon != nil {
		next = frame(soon.remaining(now), soon.total)
	}
	if slices.Equal(t.shown, next) {
		return
	}
	t.shown = next

	if next == nil {
		t.countdown.Clear()
		return
	}
	t.countdown.Paint(next)
}

// soonest is the running timer with the least left. A paused one is shown as nothing rather than as a
// ring that has stopped moving, because a paused timer is not counting and the light underneath says
// more.
func (t *Timers) soonest(now time.Time) *timer {
	var soon *timer
	for _, c := range t.held {
		if !c.active {
			continue
		}
		if soon == nil || c.remaining(now) < soon.remaining(now) {
			soon = c
		}
	}
	return soon
}

// frame is how much is left, drawn as a fraction of the ring: whole segments for the part still to
// run and the leading one dimmed by the fraction it holds, the same shape Voice PE draws.
func frame(left, total time.Duration) []led.Color {
	lit := float64(led.Segments) * left.Seconds() / math.Max(total.Seconds(), 1)

	out := make([]led.Color, led.Segments)
	for i := range out {
		switch part := lit - float64(i); {
		case part >= 1:
			out[i] = countdownColor
		case part > 0:
			out[i] = dim(countdownColor, part)
		}
	}
	return out
}

func dim(c led.Color, by float64) led.Color {
	scale := func(v byte) byte { return byte(math.Round(float64(v) * by)) }
	return led.Color{R: scale(c.R), G: scale(c.G), B: scale(c.B)}
}
