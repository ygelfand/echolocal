package voice

import (
	"context"
	"log/slog"
	"math"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/activity"
	"github.com/ygelfand/echolocal/internal/feature/feedback"
	"github.com/ygelfand/echolocal/internal/feature/light"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/mute"
	"github.com/ygelfand/echolocal/internal/feature/wakeword"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/lib/safe"
	"github.com/ygelfand/echolocal/internal/lib/wake"
)

// errDuplicate is what Home Assistant reports to the devices that lost a race to answer: "Duplicate
// wake-up detected for Glados". Every satellite in earshot hears the wake word and starts a turn, and
// only the first is served.
const errDuplicate = "duplicate_wake_up_detected"

// yieldFlash is how long the ring says the turn went elsewhere. Long enough to be seen by somebody
// looking at the wrong device, short enough not to compete with the one that is answering.
const yieldFlash = 900 * time.Millisecond

// phase is what the conversation is doing. It is the whole of its state: everything that used to be
// inferred from a handful of booleans is a phase, and every transition happens in one goroutine, so
// there is no combination to get into that the transitions do not describe.
type phase int

const (
	// phaseIdle is nothing happening.
	phaseIdle phase = iota

	// phaseListening is microphone audio going up to Home Assistant.
	phaseListening

	// phaseThinking is having stopped listening and waiting on an answer.
	phaseThinking

	// phaseReplying is speaking. The reply outlives the turn as far as Home Assistant is concerned:
	// it ends the run as soon as it has handed over the text, which is before the audio exists.
	phaseReplying
)

func (p phase) String() string {
	switch p {
	case phaseListening:
		return "listening"
	case phaseThinking:
		return "thinking"
	case phaseReplying:
		return "replying"
	}
	return "idle"
}

type eventKind int

const (
	evStart       eventKind = iota // a wake word fired, or something asked for a turn
	evHeard                        // Home Assistant has heard enough
	evReplyText                    // a reply is coming
	evReplyURL                     // the reply can be fetched whole
	evStreamStart                  // reply audio is about to arrive over the API
	evStreamAudio                  // a chunk of it
	evStreamEnd                    // all of it has been sent
	evPlayed                       // the reply has finished playing
	evRunEnd                       // Home Assistant closed the run
	evError                        // the pipeline failed
	evCancel                       // the user gave up on it
	evTimeout                      // Home Assistant never closed the run
	evPending                      // a turn held back for the last one to close has waited long enough
	evPlaying                      // the reply has audio, so the pipeline owes nothing more
	evContinue                     // Home Assistant wants the answer to a question it just asked
)

type event struct {
	kind  eventKind
	slot  int
	text  string
	url   string
	audio []byte
	code  string
	msg   string
	at    time.Time
}

// conversation runs one turn at a time: microphone audio up, pipeline events back, spoken reply out.
//
// One goroutine owns the phase and everything derived from it. Everything else — the API read loop,
// the audio streamer, the reply fetch, the buttons — posts events, so no state is read while it is
// being written and an event that does not fit the phase is dropped by the transition rather than by
// a guard flag.
type conversation struct {
	vs      *esphome.VoiceSatellite
	source  *mic.Source
	speaker *speaker.Player

	// sound is who may make one. Everything audible goes through it, so cancelling is one call
	// wherever the sound came from.
	sound  *speaker.Driver
	player *media.Player
	ring   *light.Light
	leds   *led.Driver
	log    *activity.Log

	events chan event

	// visible is the phase, published for anything outside the loop that needs to ask. Only the loop
	// writes it, and a reader tolerates being a moment out of date: the button uses it to choose
	// between starting and cancelling, and posting either is safe whichever it picks.
	visible atomic.Int32

	// Below here belongs to the run goroutine alone.
	phase phase

	// slot is the wake word slot that owns the turn, which decides the tone, the animation and the
	// pipeline. A wake from another slot takes it over rather than being ignored, so the feedback
	// follows the word that actually just fired.
	slot int

	claim     *led.Claim
	stopAudio func()
	deadline  *time.Timer

	// holding is whether the turn has taken the speaker from a track, so that taking it twice or
	// giving it back twice cannot happen however the turn ends.
	holding bool

	// followUp says this turn was opened without a wake word, so hearing nothing is a normal ending
	// rather than Home Assistant having gone away.
	followUp bool

	// pending is a turn owed once the current one has finished closing, nil when none.
	//
	// Home Assistant's events carry no run identifier, so a turn started while the last one is still
	// ending cannot tell that one's events from its own — and the first thing to arrive is the old
	// run's RUN_END, which would end the new turn instead. Waiting for the old run to close makes
	// that event the go-ahead rather than a stray.
	pending *nextTurn
	grace   *time.Timer

	reply reply
}

// graceStart is how long to wait for a stopped run to close before starting the next one anyway.
const graceStart = 750 * time.Millisecond

// nextTurn is a turn to open: which slot, and whether it follows a reply rather than a wake word.
type nextTurn struct {
	slot     int
	followUp bool
}

// reply is which answer is being spoken, and how. Only one of them is set: a url means it is being
// fetched whole, a stream means it is arriving over the API. Both play under one claim on the speaker.
type reply struct {
	url    string
	stream *stream
}

func newConversation(vs *esphome.VoiceSatellite) *conversation {
	c := &conversation{
		vs:      vs,
		source:  mic.Get(),
		speaker: speaker.Get(),
		sound:   speaker.Sound(),
		player:  media.Get(),
		ring:    light.Get(),
		leds:    led.Get(),
		log:     activity.Get(),
		events:  make(chan event, 32),
	}

	vs.OnPipelineEvent = c.pipeline
	vs.OnTTSAudio = c.tts
	vs.OnAnnounce = c.announce
	vs.OnStartRequestAccepted = func(port uint32, failed bool) {
		if failed {
			slog.Error("home assistant refused the turn")
			c.post(event{kind: evError, code: "refused"})
			return
		}
		if port != 0 {
			// The legacy UDP path is not implemented; audio goes over the API.
			slog.Warn("home assistant asked for udp audio", "port", port)
		}
	}
	return c
}

// post hands an event to the run loop. It never blocks: an event dropped because the queue is full
// is better than stalling the connection's read loop.
func (c *conversation) post(e event) {
	select {
	case c.events <- e:
	default:
		slog.Warn("conversation event dropped", "kind", e.kind, "phase", c.Phase())
	}
}

// Phase is what it is doing, for anything outside that needs to know.
func (c *conversation) Phase() phase { return phase(c.visible.Load()) }

// Run owns the conversation until ctx is cancelled.
func (c *conversation) Run(ctx context.Context) {
	c.claim = c.leds.Claim(led.PriorityTurn)
	defer c.claim.Release()

	for {
		var expired, waited <-chan time.Time
		if c.deadline != nil {
			expired = c.deadline.C
		}
		if c.grace != nil {
			waited = c.grace.C
		}

		select {
		case <-ctx.Done():
			c.handle(event{kind: evCancel})
			return
		case <-expired:
			c.handle(event{kind: evTimeout})
		case <-waited:
			c.handle(event{kind: evPending})
		case e := <-c.events:
			c.handle(e)
		}
	}
}

// handle is the transition table. Anything an event cannot do from the current phase is dropped
// here, which is what makes the whole thing idempotent.
func (c *conversation) handle(e event) {
	switch e.kind {
	case evStart:
		c.start(nextTurn{slot: e.slot})

	case evContinue:
		// The pipeline asked a question. Its answer is owed after the reply has been spoken, so this
		// only records the intent; evPlayed opens it.
		if c.phase != phaseIdle {
			slog.Info("pipeline asked for an answer", "slot", c.slot+1)
			c.pending = &nextTurn{slot: c.slot, followUp: true}
		}

	case evHeard:
		if e.text != "" {
			slog.Info("heard", "slot", c.slot+1, "text", e.text)
			c.log.Heard(e.text)
		}
		if c.phase == phaseListening {
			c.think()
		}

	case evReplyText:
		slog.Info("replying", "slot", c.slot+1, "text", e.text)
		c.log.Replied(e.text)
		if c.phase == phaseListening {
			c.think()
		}
		c.player.Sounding(true)

	case evStreamStart:
		// The turn has to be claimed before the audio arrives: Home Assistant closes the run as soon
		// as it has handed over the text, and a turn still only thinking would take that as the end
		// and go idle, discarding everything that followed.
		if c.phase != phaseIdle && wakeword.Delivery(c.slot) == config.DeliveryStream {
			c.speak("")
		}

	case evReplyURL:
		// Home Assistant serves the reply whole as well as streaming it over the API, and this
		// arrives before the first streamed byte. Fetching cannot gap: the streamed copy arrives at
		// about the rate it plays out, so any hiccup empties the queue and silence lands in the
		// middle of a word. HTTP also says when the audio has ended, which the stream does not.
		//
		// Which of the two runs is the slot's setting, so ignoring the url here leaves the streamed
		// copy to arrive on its own, exactly as it would have if Home Assistant had sent no url.
		if c.phase == phaseIdle {
			return
		}
		if wakeword.Delivery(c.slot) == config.DeliveryStream {
			return
		}
		c.speak(e.url)

	case evStreamAudio:
		// Retired as soon as a url arrives.
		if c.phase == phaseIdle || c.reply.url != "" {
			return
		}
		if c.phase != phaseReplying {
			c.speak("")
		}
		c.reply.stream.send(e.audio)

	case evStreamEnd:
		// Everything Home Assistant is going to send has been sent. What the errand is still holding
		// back for the cushion plays, and it finishes once that has been heard.
		if c.phase == phaseReplying && c.reply.stream != nil {
			c.reply.stream.done()
		}

	case evPlayed:
		c.reported(e.at)
		if c.phase == phaseReplying {
			slot := c.slot
			c.idle("spoken")

			// Continual conversation: the slot keeps listening after every reply, not only the ones
			// Home Assistant asked to continue.
			if c.pending == nil && wakeword.FollowUp(slot) > 0 {
				slog.Info("listening again after the reply", "slot", slot+1, "for", wakeword.FollowUp(slot))
				c.pending = &nextTurn{slot: slot, followUp: true}
			}
			c.startPending()
		}

	case evRunEnd:
		// While idle this is the stopped run closing, which is what a held-back turn is waiting for.
		if c.phase == phaseIdle {
			c.startPending()
			return
		}
		// A reply is on its way or playing and owns the ring and the player until it has been heard.
		if c.phase != phaseReplying {
			c.idle("ended")
		}

	case evPending:
		c.startPending()

	case evPlaying:
		c.disarm()

	case evError:
		// Two devices in earshot both hear the wake word and both start a turn. Home Assistant keeps the
		// first and refuses the rest, so this arrives on every device that lost — which is not a failure
		// of any of them, and a chime and a red ring say the opposite to the room. It ends the turn and
		// says nothing: the device that won is about to light up and answer.
		if e.code == errDuplicate {
			slog.Info("another device answered first", "slot", c.slot+1, "message", e.msg)
			c.clearPending()
			c.idle("answered elsewhere")
			c.leds.Busy().Flash(led.WorkElsewhere, yieldFlash)
			return
		}

		slog.Error("pipeline error", "slot", c.slot+1, "code", e.code, "message", e.msg)
		c.clearPending()
		c.idle("failed")
		c.trouble()

	case evCancel:
		c.clearPending()

		// Stopping the sound comes first and does not care whether a turn is open: an announcement
		// plays with nothing else happening, and used to be the one thing cancel could not reach.
		sounding := c.sound.Busy()
		c.sound.Silence()

		// A track is not one sound and outlives any claim, so stopping it is its own call. Cancel ends
		// it rather than pausing it: nothing is coming back to carry on from.
		playing, paused := c.player.Playing()
		c.player.Stop()

		if c.phase == phaseIdle && !sounding && !playing && !paused {
			return
		}
		slog.Info("cancelled", "phase", c.phase, "slot", c.slot+1, "sounding", sounding)
		c.idle("cancelled")
		feedback.Cancelled()

	case evTimeout:
		c.clearPending()

		// Nobody spoke into a turn nobody asked for, which is how a follow-up is meant to end. Every
		// other timeout is something failing: listening means Home Assistant stopped answering,
		// thinking means its pipeline is slower than the slot allows for.
		if c.followUp && c.phase == phaseListening {
			slog.Info("nothing followed", "slot", c.slot+1)
			c.idle("nothing said")
			return
		}

		switch c.phase {
		case phaseListening:
			slog.Warn("gave up listening", "slot", c.slot+1, "after", wakeword.MaxListen(c.slot))
		case phaseThinking:
			slog.Warn("gave up waiting for an answer", "slot", c.slot+1, "after", wakeword.MaxThink(c.slot))
		}
		c.idle("timed out")
		c.trouble()
	}
}

// muted reports whether the microphones are cut. A device that could not take the pin has none.
func (c *conversation) muted() (bool, error) { return mute.Get().Muted() }

// start opens a turn on the pipeline paired with a slot.
func (c *conversation) start(n nextTurn) {
	slot := n.slot

	// A cut microphone hears nothing, so a turn started from a button would stream silence until it
	// gave up — and worse, would look like the device is listening while it is muted.
	if muted, err := c.muted(); err == nil && muted {
		slog.Info("microphone muted, not starting a turn", "slot", slot+1)
		c.trouble()
		return
	}

	// Saying the wake word while the device is still listening is part of the sentence, not a new
	// request: acting on it would cut off what the user is in the middle of saying. Once it is
	// thinking or speaking, a wake word is a deliberate interruption and takes the turn over.
	if c.phase == phaseListening {
		slog.Info("wake word ignored, still listening", "slot", slot+1)
		return
	}

	if c.phase != phaseIdle {
		slog.Info("conversation interrupted", "was", c.phase, "from", c.slot+1, "slot", slot+1)
		c.sound.Silence()

		// Held back before the turn ends, so that ending it does not hand the speaker back to a track
		// for the moment it takes the next turn to open.
		c.pending = &nextTurn{slot: slot}
		c.idle("interrupted")

		// The stopped run has yet to close, and its last events are still on their way.
		c.grace = time.NewTimer(graceStart)
		return
	}
	c.clearPending()

	if !c.vs.Subscribed() {
		slog.Warn("no voice pipeline subscribed, ignoring wake", "slot", slot+1)
		wakeword.Chime(slot)
		c.trouble()
		return
	}

	// Which pipeline runs is resolved from the phrase, so a slot with no wake word in it cannot be
	// reached: an empty phrase means the first pipeline. That is right for slot 0, where no wake word
	// is a legitimate way to open a turn, and wrong for any other, where it would quietly run the
	// wrong assistant instead of saying it could not run the one asked for.
	phrase, ok := c.phraseFor(slot)
	if slot > 0 && !ok {
		slog.Warn("no wake word in that slot, nothing to reach its pipeline with", "slot", slot+1)
		c.trouble()
		return
	}

	c.slot = slot
	c.followUp = n.followUp

	// A follow-up chimes like any other turn: the microphone is open with nothing said to say so.
	// It is not a wake, though, so it does not report a phrase nobody spoke.
	wakeword.Chime(slot)
	if !n.followUp {
		c.log.Woke(phrase)
	}
	if err := c.vs.StartTurn(phrase, audioSettings()); err != nil {
		slog.Error("starting the turn failed", "slot", slot+1, "err", err)
		c.trouble()
		return
	}

	c.enter(phaseListening)
	c.reply = reply{}

	c.hold(true)

	if effect := wakeword.Effect(slot); effect != "" {
		c.claim.Play(effect, c.ring.Base())
	}

	c.arm(c.listenFor(n))
	c.startAudio(slot)
	// The phrase is logged for a follow-up too, because it is what chose the pipeline — not because
	// anyone said it.
	slog.Info("turn started", "slot", slot+1, "phrase", phrase, "follow_up", n.followUp)
}

// listenFor is how long a turn may listen. A follow-up gets its own, shorter, window when the slot
// sets one: it was not asked for, so silence is a likely answer and holding the microphone open
// through the full limit is what the wake word is there to avoid.
func (c *conversation) listenFor(n nextTurn) time.Duration {
	if n.followUp {
		if d := wakeword.FollowUp(n.slot); d > 0 {
			return d
		}
	}
	return wakeword.MaxListen(n.slot)
}

// think stops sending audio. The same animation turned around says the device has stopped listening
// and is waiting on an answer, which is a different thing to be doing and looks like one.
func (c *conversation) think() {
	c.stopStreaming()
	c.enter(phaseThinking)
	c.arm(wakeword.MaxThink(c.slot))

	if effect := wakeword.Effect(c.slot); effect != "" {
		c.claim.PlayReversed(effect, c.ring.Base())
	}
}

// speak moves to playing the reply. url is empty when it is arriving over the API instead.
//
// Either way it becomes one claim on the speaker, so silencing it stops the whole errand: a fetch
// still downloading is abandoned rather than arriving to play into a cancelled turn, and a stream
// still receiving stops taking chunks.
func (c *conversation) speak(url string) {
	c.stopStreaming()
	c.enter(phaseReplying)
	c.reply = reply{url: url}

	if effect := wakeword.Effect(c.slot); effect != "" {
		c.claim.PlayReversed(effect, c.ring.Base())
	}
	c.player.Sounding(true)

	// The deadline is left as it was. Text arriving is not the pipeline delivering: it still owes the
	// audio, and the limit it was given when the device stopped listening goes on running until some
	// of that audio turns up.
	errand := func(ctx context.Context, p *speaker.Player) error { return c.play(ctx, url) }
	if url == "" {
		s := newStream(c.speaker.Splices(), wakeword.Buffer(c.slot))
		c.reply.stream = s
		errand = func(ctx context.Context, p *speaker.Player) error {
			return s.play(ctx, p, func() { c.post(event{kind: evPlaying}) })
		}
	}

	held := c.sound.Claim("reply", errand)
	safe.Go("reply", func() {
		<-held.Done()

		if err := held.Err(); err != nil {
			slog.Error("playing the reply failed", "url", url, "err", err)
		}
		if held.Stopped() {
			return
		}
		c.post(event{kind: evPlayed, at: held.Quiet()})
	})
}

// idle puts everything back. why is only for the log.
func (c *conversation) idle(why string) {
	was := c.phase
	c.stopStreaming()
	c.disarm()

	if was != phaseIdle {
		_ = c.vs.StopTurn()
	}

	c.enter(phaseIdle)
	c.claim.Clear()
	c.player.Sounding(false)
	c.reply = reply{}
	c.hold(c.pending != nil)

	if was != phaseIdle {
		slog.Info("turn ended", "was", was, "slot", c.slot+1, "why", why)
	}
}

func (c *conversation) enter(p phase) {
	c.phase = p
	c.visible.Store(int32(p))
}

// startPending opens the turn that was held back, if there is one.
func (c *conversation) startPending() {
	next := c.pending
	c.clearPending()

	if next != nil {
		c.start(*next)
	}
}

func (c *conversation) clearPending() {
	c.pending = nil
	if c.grace != nil {
		c.grace.Stop()
		c.grace = nil
	}

	// A turn that is not going to open cannot be what a track is waiting for.
	if c.phase == phaseIdle {
		c.hold(false)
	}
}

// hold takes the speaker away from a track for as long as a turn needs it, and is what gives it
// back. A turn is several sounds with listening in between, so it holds it across all of them
// rather than one claim at a time.
func (c *conversation) hold(on bool) {
	if on == c.holding {
		return
	}
	c.holding = on

	if on {
		c.player.Duck()
		return
	}
	c.player.Unduck()
}

// arm gives the current phase a limit; disarm removes it. Each phase sets its own, so a slow model
// does not eat the time the microphone was meant to have and a live microphone is not bounded by how
// long an answer may take.
func (c *conversation) arm(d time.Duration) {
	c.disarm()
	c.deadline = time.NewTimer(d)
}

func (c *conversation) disarm() {
	if c.deadline != nil {
		c.deadline.Stop()
		c.deadline = nil
	}
}

// trouble says a request could not be served. It takes its own claim at a priority above the turn,
// so ending the turn that failed cannot take the indication away with it.
func (c *conversation) trouble() {
	feedback.Failure()
}

// phraseFor is what Home Assistant expects a turn to report for one of its wake word slots: the
// spoken phrase, not the model's id. Which pipeline runs is resolved by comparing that phrase against
// each slot's select, so a turn from slot n has to report slot n's own phrase.
func (c *conversation) phraseFor(slot int) (string, bool) {
	if slot < 0 || slot >= len(c.vs.ActiveWakeWords) {
		return "", false
	}

	// From what is on disk, because a slot is only active once its model loaded, and the advertised list
	// is not kept anywhere to be read.
	id := c.vs.ActiveWakeWords[slot]
	if m, ok := wake.Find(wake.Lib().Ours(), id); ok {
		return m.Phrase, true
	}
	return id, true
}

// pipeline turns what Home Assistant reports into events. It runs on the connection's read loop, so
// it does nothing but translate.
func (c *conversation) pipeline(e esphome.PipelineEvent) {
	switch e.Type {
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_STT_END:
		c.post(event{kind: evHeard, text: e.Data["text"]})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_STT_VAD_END:
		c.post(event{kind: evHeard})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_PROGRESS:
		if e.Data["tts_start_streaming"] == "1" {
			slog.Info("early tts streaming offered", "slot", c.slot+1)
		}

	case api.VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_END:
		if e.Data["continue_conversation"] == "1" {
			c.post(event{kind: evContinue})
		}
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_START:
		c.post(event{kind: evReplyText, text: e.Data["text"]})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_END:
		if url := e.Data["url"]; url != "" {
			c.post(event{kind: evReplyURL, url: url})
		}

	// Home Assistant brackets the streamed audio with these, and calls the reply finished after the
	// end. They are the reliable signals: the per-message end flag is not.
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_START:
		c.post(event{kind: evStreamStart})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_END:
		c.post(event{kind: evStreamEnd})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END:
		c.post(event{kind: evRunEnd})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_ERROR:
		c.post(event{kind: evError, code: e.Data["code"], msg: e.Data["message"]})
	}
}

// tts is reply audio streamed over the API, as 16 kHz mono.
func (c *conversation) tts(data []byte, end bool) {
	if len(data) > 0 {
		c.post(event{kind: evStreamAudio, audio: data})
	}
	// The per-message end flag is not depended on: TTS_STREAM_END is what Home Assistant sends when it
	// has finished, and it arrives whether or not this was ever set.
	_ = end
}

// Start asks for a turn on a slot's pipeline. Wake detection and the buttons both use it.
func (c *conversation) Start(slot int) { c.post(event{kind: evStart, slot: slot}) }

// Cancel gives up on whatever is happening.
func (c *conversation) Cancel() { c.post(event{kind: evCancel}) }

// Busy reports whether there is anything to cancel.
func (c *conversation) Busy() bool { return c.Phase() != phaseIdle }

// audioSettings asks Home Assistant to condition the microphone audio before recognition. The
// array's analogue gain already matches what the vendor's own recogniser ran with, and speech still
// reaches the pipeline around -45 dBFS, which is some 20 dB below what a recogniser wants: the vendor
// made that up in its DSP, and this is the equivalent handled by the far end.
//
// Auto gain is a ceiling on how much the far end may apply, not a fixed boost, so it costs nothing
// when the talker is close and loud.
func audioSettings() *api.VoiceAssistantAudioSettings {
	return &api.VoiceAssistantAudioSettings{
		NoiseSuppressionLevel: 2,
		AutoGain:              31,
		VolumeMultiplier:      1,
	}
}

// startAudio begins sending microphone frames. The streamer only reads, so it needs no coordination
// beyond being told to stop.
func (c *conversation) startAudio(slot int) {
	ctx, cancel := context.WithCancel(context.Background())
	c.stopAudio = cancel

	// slot is captured rather than read from the loop's state, which the streamer does not own.
	safe.Go("turn audio", func() { c.stream(ctx, slot) })
}

func (c *conversation) stopStreaming() {
	if c.stopAudio == nil {
		return
	}
	c.stopAudio()
	c.stopAudio = nil

	if err := c.vs.EndAudio(); err != nil {
		slog.Error("ending audio failed", "err", err)
	}
}

// stream sends microphone frames until it is told to stop.
func (c *conversation) stream(ctx context.Context, slot int) {
	frames, unlisten := c.source.Listen()
	defer unlisten()

	buf := make([]byte, 0, mic.FrameSamples*2)

	// Send what was already said before this turn began. The wake word can only be recognised after
	// it has been spoken, and people run straight on into the request, so the opening words exist only
	// in the microphone's history. Subscribing first means no audio falls between the two.
	//
	// A slot that chimes does not send it: whoever is talking waits for the tone, so the history holds
	// the wake word rather than the request, and the tone itself is louder at the array than they are.
	pre := c.source.Recent(mic.History)
	if wakeword.Tones(slot) {
		pre = nil
	}
	if len(pre) > 0 {
		for _, s := range pre {
			buf = append(buf, byte(s), byte(s>>8))
		}
		if err := c.vs.SendAudio(buf); err != nil {
			slog.Error("sending audio history failed", "err", err)
			return
		}
		slog.Debug("sent audio history", "ms", len(pre)*1000/mic.Rate)
	}

	// What the microphone actually sends is the input to speech recognition, and a quiet or clipped
	// stream explains a bad transcript better than anything downstream does.
	// The tone plays as the turn opens and is louder at the array than a talker across the room, so
	// nothing is sent while it is sounding. What the speaker still has queued says when that is, and
	// hardwareTail is what the driver holds after the queue runs out.
	playing := c.speaker != nil && c.speaker.Queued() > 0
	var quiet time.Time
	var held int

	var peak, samples int
	var energy float64
	defer func() {
		if samples == 0 {
			return
		}
		rms := math.Sqrt(energy / float64(samples))
		slog.Info("sent audio",
			"slot", slot+1,
			"seconds", float64(samples)/float64(mic.Rate),
			"peak", peak,
			"peakdbfs", math.Round(20*math.Log10(max(float64(peak), 1)/32768)*10)/10,
			"rms", math.Round(rms),
			"rmsdbfs", math.Round(20*math.Log10(math.Max(rms, 1)/32768)*10)/10,
			"gaindb", math.Round(c.source.Gain()*10)/10,
			"clipped", c.source.Clipped())
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}

			if playing {
				switch {
				case c.speaker.Queued() > 0:
					quiet = time.Time{}
				case quiet.IsZero():
					quiet = time.Now()
				case time.Since(quiet) >= speaker.HardwareTail:
					playing = false
					slog.Debug("held the tone back", "ms", held*1000/mic.Rate)
				}
				if playing {
					held += len(frame)
					continue
				}
			}

			buf = buf[:0]
			for _, s := range frame {
				buf = append(buf, byte(s), byte(s>>8))
				peak = max(peak, int(s), -int(s))
				energy += float64(s) * float64(s)
				samples++
			}
			if err := c.vs.SendAudio(buf); err != nil {
				slog.Error("sending audio failed", "err", err)
				return
			}
		}
	}
}

// reported logs how a streamed reply went, once the errand that played it has finished and its
// counters are nobody's to write. gap is silence the device had nothing to fill with.
func (c *conversation) reported(dry time.Time) {
	s := c.reply.stream
	if s == nil || s.bytes == 0 || s.playingAt.IsZero() {
		return
	}

	seconds := float64(s.bytes) / float64(2*mic.Rate)
	took := dry.Sub(s.playingAt).Seconds()

	slog.Info("reply played",
		"via", wakeword.Delivery(c.slot),
		"bytes", s.bytes,
		"seconds", math.Round(seconds*100)/100,
		"took", math.Round(took*100)/100,
		"gap", math.Round(max(took-seconds, 0)*100)/100,
		"peak", s.peak,
		"seams", c.speaker.Splices()-s.splicesAt,
		"clipped", c.speaker.Clipped(),
		"dropped", c.source.Dropped())
}

// activeWakeWords is the per-slot selection the user last made, filtered to what the device can
// actually load. Home Assistant takes it as authoritative, so claiming a model that is not here would
// leave a slot looking armed and deaf.
func activeWakeWords(models []wake.Model, slots int) []string {
	saved := config.Get().Wake
	var active []string
	for i := range slots {
		if id := saved.Slot(i).ID; id != "" {
			if _, ok := wake.Find(models, id); ok {
				active = append(active, id)
			}
		}
	}

	// Nothing chosen yet: start listening for something rather than nothing, or a fresh device looks
	// broken until the user finds the select. The shipped default when it is installed, and otherwise
	// whatever this device does have — a device carrying one model somebody copied on should listen for
	// that one rather than for nothing.
	if len(active) == 0 {
		if m, ok := wake.Find(models, wake.DefaultModel); ok {
			active = []string{m.ID}
		} else if len(models) > 0 {
			active = []string{models[0].ID}
		}
	}
	return active
}
