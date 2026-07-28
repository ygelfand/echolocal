package satellite

import (
	"context"
	"log/slog"
	"math"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/ygelfand/echolocal/internal/alog"
	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/speaker"
	"github.com/ygelfand/echolocal/internal/wake"
)

// turnTimeout ends a turn that Home Assistant never closes, so a lost reply cannot leave the device
// streaming the room indefinitely.
const turnTimeout = 30 * time.Second

// troubleFlash is how long the ring shows a failure, and troubleColor what it shows. Red is not used
// anywhere else, so it never has to be told apart from an effect or a volume arc.
const troubleFlash = 1500 * time.Millisecond

var troubleColor = led.Color{R: 0xC0, G: 0x00, B: 0x00}

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
	evStreamAudio                  // reply audio over the API, the fallback path
	evStreamEnd
	evFlush   // play whatever is held back, for a reply shorter than the cushion
	evPlayed  // the reply has finished playing
	evRunEnd  // Home Assistant closed the run
	evError   // the pipeline failed
	evCancel  // the user gave up on it
	evTimeout // Home Assistant never closed the run
)

type event struct {
	kind  eventKind
	slot  int
	text  string
	url   string
	audio []byte
	code  string
	msg   string
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
	player  *mediaPlayer
	wake    *wakeControl
	ring    *ringLight
	leds    *led.Driver

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

	reply reply
}

// reply is the state of the answer being spoken. It only means anything in phaseReplying.
type reply struct {
	// url is set when the reply is being fetched whole, which retires the streamed copy.
	url string

	held    []int16 // audio waiting for the cushion to fill
	flowing bool    // the cushion is built and audio is going straight through

	bytes     int
	peak      int
	splicesAt uint64 // seam count when the reply started, so only this reply's are reported
}

func newConversation(vs *esphome.VoiceSatellite, source *mic.Source, spk *speaker.Player,
	ring *ringLight, leds *led.Driver, player *mediaPlayer, wakeCtl *wakeControl) *conversation {

	c := &conversation{
		vs:      vs,
		source:  source,
		speaker: spk,
		player:  player,
		wake:    wakeCtl,
		ring:    ring,
		leds:    leds,
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
		var expired <-chan time.Time
		if c.deadline != nil {
			expired = c.deadline.C
		}

		select {
		case <-ctx.Done():
			c.handle(event{kind: evCancel})
			return
		case <-expired:
			c.handle(event{kind: evTimeout})
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
		c.start(e.slot)

	case evHeard:
		if e.text != "" {
			slog.Info("heard", "text", e.text)
		}
		if c.phase == phaseListening {
			c.think()
		}

	case evReplyText:
		slog.Info("replying", "text", e.text)
		if c.phase == phaseListening {
			c.think()
		}
		c.player.mp.SetState(esphome.MediaPlayerAnnouncing)

	case evReplyURL:
		// Home Assistant serves the reply whole as well as streaming it over the API, and this
		// arrives before the first streamed byte. Fetching cannot gap: the streamed copy arrives at
		// about the rate it plays out, so any hiccup empties the queue and silence lands in the
		// middle of a word. HTTP also says when the audio has ended, which the stream does not.
		if c.phase == phaseIdle {
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
		c.queue(e.audio)

	case evStreamEnd:
		if c.phase == phaseReplying && c.reply.url == "" {
			go alog.Safely("reply drain", c.awaitPlayback)
		}

	case evFlush:
		c.flush()

	case evPlayed:
		c.reported()
		if c.phase == phaseReplying {
			c.idle("spoken")
		}

	case evRunEnd:
		// A reply is on its way or playing and owns the ring and the player until it has been heard.
		if c.phase != phaseReplying {
			c.idle("ended")
		}

	case evError:
		slog.Error("pipeline error", "code", e.code, "message", e.msg)
		c.idle("failed")
		c.trouble()

	case evCancel:
		if c.phase == phaseIdle {
			return
		}
		slog.Info("conversation cancelled", "phase", c.phase, "slot", c.slot+1)
		if c.speaker != nil {
			c.speaker.Drain()
		}
		c.idle("cancelled")
		chime(c.speaker, toneCancel)

	case evTimeout:
		slog.Warn("turn timed out", "phase", c.phase)
		c.idle("timed out")
		c.trouble()
	}
}

// start opens a turn on the pipeline paired with slot.
func (c *conversation) start(slot int) {
	// Saying the wake word while the device is still listening is part of the sentence, not a new
	// request: acting on it would cut off what the user is in the middle of saying. Once it is
	// thinking or speaking, a wake word is a deliberate interruption and takes the turn over.
	if c.phase == phaseListening {
		slog.Info("wake word ignored, still listening", "slot", slot+1)
		return
	}

	if c.phase != phaseIdle {
		slog.Info("conversation interrupted", "was", c.phase, "wasslot", c.slot+1, "slot", slot+1)
		if c.speaker != nil {
			c.speaker.Drain()
		}
		c.idle("interrupted")
	}

	if !c.vs.Subscribed() {
		slog.Warn("no voice pipeline subscribed, ignoring wake")
		c.wake.Chime(slot)
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
	c.wake.Chime(slot)
	if err := c.vs.StartTurn(phrase, audioSettings()); err != nil {
		slog.Error("starting the turn failed", "err", err)
		c.trouble()
		return
	}

	c.enter(phaseListening)
	c.reply = reply{}
	c.player.mp.SetState(esphome.MediaPlayerIdle)

	if effect := c.wake.Effect(slot); effect != "" {
		c.claim.Play(effect, c.ring.Base())
	}

	c.deadline = time.NewTimer(turnTimeout)
	c.startAudio()
	slog.Info("turn started", "slot", slot+1, "phrase", phrase)
}

// think stops sending audio. The same animation turned around says the device has stopped listening
// and is waiting on an answer, which is a different thing to be doing and looks like one.
func (c *conversation) think() {
	c.stopStreaming()
	c.enter(phaseThinking)

	if effect := c.wake.Effect(c.slot); effect != "" {
		c.claim.PlayReversed(effect, c.ring.Base())
	}
}

// speak moves to playing the reply. url is empty for the streamed fallback.
func (c *conversation) speak(url string) {
	c.stopStreaming()
	c.enter(phaseReplying)
	c.reply.url = url

	if effect := c.wake.Effect(c.slot); effect != "" {
		c.claim.PlayReversed(effect, c.ring.Base())
	}
	c.player.mp.SetState(esphome.MediaPlayerAnnouncing)

	// The turn's own deadline no longer applies: how long a reply takes is not the device's business.
	c.stopDeadline()

	if url == "" {
		c.reply.splicesAt = c.speaker.Splices()
		go alog.Safely("reply report", c.report)
		return
	}

	c.reply.splicesAt = c.speaker.Splices()
	go alog.Safely("reply", func() {
		if err := c.play(url); err != nil {
			slog.Error("fetching the reply failed", "url", url, "err", err)
		}
		c.post(event{kind: evPlayed})
	})
}

// idle puts everything back. why is only for the log.
func (c *conversation) idle(why string) {
	was := c.phase
	c.stopStreaming()
	c.stopDeadline()

	if was != phaseIdle {
		_ = c.vs.StopTurn()
	}

	c.enter(phaseIdle)
	c.claim.Clear()
	c.player.mp.SetState(esphome.MediaPlayerIdle)
	c.reply = reply{}

	if was != phaseIdle {
		slog.Info("turn ended", "was", was, "slot", c.slot+1, "why", why)
	}
}

func (c *conversation) enter(p phase) {
	c.phase = p
	c.visible.Store(int32(p))
}

func (c *conversation) stopDeadline() {
	if c.deadline != nil {
		c.deadline.Stop()
		c.deadline = nil
	}
}

// trouble says a request could not be served. It takes its own claim at a priority above the turn,
// so ending the turn that failed cannot take the indication away with it.
func (c *conversation) trouble() {
	chime(c.speaker, toneTrouble)

	frame := make([]led.Color, led.Segments)
	for i := range frame {
		frame[i] = troubleColor
	}
	c.leds.Claim(led.PriorityTrouble).PaintFor(frame, troubleFlash)
}

// phraseFor is what Home Assistant expects a turn to report for one of its wake word slots: the
// spoken phrase, not the model's id. Which pipeline runs is resolved by comparing that phrase against
// each slot's select, so a turn from slot n has to report slot n's own phrase.
func (c *conversation) phraseFor(slot int) (string, bool) {
	if slot < 0 || slot >= len(c.vs.ActiveWakeWords) {
		return "", false
	}

	id := c.vs.ActiveWakeWords[slot]
	for _, w := range c.vs.AvailableWakeWords {
		if w.ID == id {
			return w.Phrase, true
		}
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
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_START:
		c.post(event{kind: evReplyText, text: e.Data["text"]})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_END:
		if url := e.Data["url"]; url != "" {
			c.post(event{kind: evReplyURL, url: url})
		}
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
	if end {
		c.post(event{kind: evStreamEnd})
	}
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
func (c *conversation) startAudio() {
	ctx, cancel := context.WithCancel(context.Background())
	c.stopAudio = cancel
	go alog.Safely("turn audio", func() { c.stream(ctx) })
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
func (c *conversation) stream(ctx context.Context) {
	frames, unlisten := c.source.Listen()
	defer unlisten()

	buf := make([]byte, 0, mic.FrameSamples*2)

	// Send what was already said before this turn began. The wake word can only be recognised after
	// it has been spoken, and people run straight on into the request, so the opening words exist only
	// in the microphone's history. Subscribing first means no audio falls between the two.
	if pre := c.source.Recent(mic.History); len(pre) > 0 {
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
	var peak, samples int
	var energy float64
	defer func() {
		if samples == 0 {
			return
		}
		rms := math.Sqrt(energy / float64(samples))
		slog.Info("sent audio",
			"seconds", float64(samples)/float64(mic.Rate),
			"peak", peak,
			"peakdbfs", math.Round(20*math.Log10(max(float64(peak), 1)/32768)*10)/10,
			"rms", math.Round(rms),
			"rmsdbfs", math.Round(20*math.Log10(math.Max(rms, 1)/32768)*10)/10)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if !ok {
				return
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

// replyCushion is how much of a reply to hold before starting to play it. The reply arrives over the
// network at roughly the rate it plays, so without a cushion every hiccup empties the queue and
// silence gets spliced into the middle of a word. It is added latency, so it is only as large as it
// needs to be to cover the jitter actually seen.
const replyCushion = 500 * time.Millisecond

// queue hands streamed audio to the speaker once enough has arrived to play through a gap. Whenever
// the queue does run empty the cushion is rebuilt, rather than dribbling out what little has arrived.
func (c *conversation) queue(data []byte) {
	samples := make([]int16, len(data)/2)
	peak := 0
	for i := range samples {
		samples[i] = int16(uint16(data[i*2]) | uint16(data[i*2+1])<<8)
		peak = max(peak, int(samples[i]), -int(samples[i]))
	}

	// How loud the reply arrives decides whether interpolating it can overflow, and the byte count
	// against how long it takes to say is how a wrong sample rate would show up.
	c.reply.bytes += len(data)
	c.reply.peak = max(c.reply.peak, peak)

	c.reply.held = append(c.reply.held, samples...)
	if c.speaker.Queued() == 0 {
		c.reply.flowing = false
	}

	cushion := int(replyCushion/time.Millisecond) * speaker.VoiceRate / 1000
	if !c.reply.flowing && len(c.reply.held) < cushion {
		return
	}

	c.reply.flowing = true
	out := c.reply.held
	c.reply.held = nil
	c.speaker.PlayVoice(out)
}

// flush plays whatever is still held, for a reply that ended before filling the cushion.
func (c *conversation) flush() {
	out := c.reply.held
	c.reply.held = nil
	if len(out) > 0 {
		c.speaker.PlayVoice(out)
	}
}

// report follows a streamed reply to the end and says how it went.
func (c *conversation) report() {
	// A reply shorter than the cushion would otherwise sit there unplayed.
	time.Sleep(replyCushion)
	c.post(event{kind: evFlush})

	c.awaitPlayback()
}

// awaitPlayback waits for the speaker to stay empty, then says the reply is done. Chunks arrive with
// gaps, so the queue emptying once does not mean the reply is over.
func (c *conversation) awaitPlayback() {
	if c.speaker == nil {
		c.post(event{kind: evPlayed})
		return
	}

	const idleFor = 8
	for idle := 0; idle < idleFor; {
		if c.speaker.Queued() == 0 {
			idle++
		} else {
			idle = 0
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.post(event{kind: evPlayed})
}

// reported logs how the reply went. One seam is the reply running out at its end; more than that is
// silence spliced into the middle of speech, which is what a reply arriving over the network slower
// than it plays out sounds like.
func (c *conversation) reported() {
	if c.reply.bytes == 0 {
		return
	}

	slog.Info("reply played",
		"bytes", c.reply.bytes,
		"seconds", float64(c.reply.bytes)/float64(2*mic.Rate),
		"peak", c.reply.peak,
		"seams", c.speaker.Splices()-c.reply.splicesAt,
		"clipped", c.speaker.Clipped(),
		"dropped", c.source.Dropped())
}

// wakeWords advertises the models the selected backend can run, with the per-slot selection the user
// last made for it. Only that backend's models are offered: the other's cannot be loaded without a
// second front end, so offering them would be offering something the device will refuse.
func wakeWords(models []wake.Model, slots int) ([]esphome.WakeWord, []string) {
	out := make([]esphome.WakeWord, 0, len(models))
	for _, m := range models {
		out = append(out, esphome.WakeWord{ID: m.ID, Phrase: m.Phrase, TrainedLanguages: m.Languages})
	}

	saved := settings.Get().Wake
	var active []string
	for i := range slots {
		if id := saved.WordID(i); id != "" {
			if _, ok := wake.Find(models, id); ok {
				active = append(active, id)
			}
		}
	}

	// Nothing saved for this backend yet: start it listening for something rather than nothing, or a
	// fresh device looks broken until the user finds the select.
	if len(active) == 0 && len(models) > 0 {
		active = []string{models[0].ID}
	}
	return out, active
}
