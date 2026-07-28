package satellite

import (
	"log/slog"
	"math"
	"sync"
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

// turnTimeout ends a turn that Home Assistant never closes, so a lost reply cannot leave the
// device streaming the room indefinitely.
const turnTimeout = 30 * time.Second

// voiceTurn runs one conversation at a time: microphone audio up, pipeline events back, spoken
// reply out. Home Assistant drives the stages; the device only decides when to start.
type voiceTurn struct {
	vs      *esphome.VoiceSatellite
	source  *mic.Source
	speaker *speaker.Player
	ring    *ringLight
	player  *mediaPlayer

	// effect is the animation the wake word started, so the turn can turn it around when it stops
	// listening. Empty means the user turned the animation off.
	effect func() string

	mu      sync.Mutex
	running bool
	stop    func()

	// wakeSlot is which of Home Assistant's wake word slots opened the turn, so the feedback that
	// runs through it is that slot's. A turn the action button opened reports slot 0.
	wakeSlot int

	replyBytes int
	replyPeak  int
	splicesAt  uint64  // seam count when the reply started, so only this reply's are reported
	held       []int16 // reply audio waiting for the cushion to fill
	flowing    bool    // the cushion is built and audio is going straight through
	replyURL   string  // set when the reply is being fetched whole, which retires the streamed copy
	replying   bool    // a reply is on its way or playing, and owns the ring until it is done
}

func newVoiceTurn(vs *esphome.VoiceSatellite, source *mic.Source, spk *speaker.Player, ring *ringLight, player *mediaPlayer, effect func() string) *voiceTurn {
	t := &voiceTurn{vs: vs, source: source, speaker: spk, ring: ring, player: player, effect: effect}

	vs.OnPipelineEvent = t.event
	vs.OnTTSAudio = t.tts
	vs.OnAnnounce = t.announce
	vs.OnStartRequestAccepted = func(port uint32, failed bool) {
		if failed {
			slog.Error("home assistant refused the turn")
			t.end()
			return
		}
		if port != 0 {
			// The legacy UDP path is not implemented; audio goes over the API.
			slog.Warn("home assistant asked for udp audio", "port", port)
		}
	}
	return t
}

// slot is which wake word slot opened the running turn.
func (t *voiceTurn) slot() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.wakeSlot
}

// Start opens a turn on the pipeline paired with slot. Wake detection calls it, and so does the
// action button.
// Start reports whether a turn opened. The caller lights the ring on a wake, so it needs to know
// when nothing is going to stop it.
func (t *voiceTurn) Start(slot int, phrase string) bool {
	if !t.vs.Subscribed() {
		slog.Warn("no voice pipeline subscribed, ignoring wake")
		t.trouble()
		return false
	}

	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		slog.Debug("turn already running")
		return false
	}
	t.running = true
	t.mu.Unlock()

	t.mu.Lock()
	t.replyURL = ""
	t.held = nil
	t.flowing = false
	t.wakeSlot = slot
	t.mu.Unlock()

	if err := t.vs.StartTurn(phrase, audioSettings()); err != nil {
		slog.Error("starting the turn failed", "err", err)
		t.end()
		return false
	}

	// The ring is left alone: whatever the wake started keeps running through listening.
	slog.Info("turn started", "slot", slot+1, "phrase", phrase)
	go alog.Safely("voice turn", t.stream)
	return true
}

// audioSettings asks Home Assistant to condition the microphone audio before recognition. The
// array's analogue gain already matches what the vendor's own recogniser ran with, and speech still
// reaches the pipeline around -45 dBFS, which is some 20 dB below what a recogniser wants: the
// vendor made that up in its DSP, and this is the equivalent handled by the far end.
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

// stream sends microphone frames until the pipeline has heard enough.
func (t *voiceTurn) stream() {
	frames, unlisten := t.source.Listen()
	defer unlisten()

	done := make(chan struct{})
	t.mu.Lock()
	t.stop = sync.OnceFunc(func() { close(done) })
	t.mu.Unlock()

	deadline := time.After(turnTimeout)
	buf := make([]byte, 0, mic.FrameSamples*2)

	// Send what was already said before this turn began. The wake word can only be recognised after
	// it has been spoken, and people run straight on into the request, so the opening words exist
	// only in the microphone's history. Subscribing first means no audio falls between the two.
	if pre := t.source.Recent(mic.History); len(pre) > 0 {
		buf = buf[:0]
		for _, s := range pre {
			buf = append(buf, byte(s), byte(s>>8))
		}
		if err := t.vs.SendAudio(buf); err != nil {
			slog.Error("sending audio history failed", "err", err)
			t.end()
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
		case <-done:
			return
		case <-deadline:
			slog.Warn("turn timed out")
			t.end()
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
			if err := t.vs.SendAudio(buf); err != nil {
				slog.Error("sending audio failed", "err", err)
				t.end()
				return
			}
		}
	}
}

// event follows Home Assistant through the pipeline's stages.
func (t *voiceTurn) event(e esphome.PipelineEvent) {
	switch e.Type {
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_STT_END:
		slog.Info("heard", "text", e.Data["text"])
		t.stopStreaming()

	case api.VoiceAssistantEvent_VOICE_ASSISTANT_STT_VAD_END:
		t.stopStreaming()

	case api.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_START:
		slog.Info("replying", "text", e.Data["text"])
		t.player.mp.SetState(esphome.MediaPlayerAnnouncing)

	case api.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_END:
		// Home Assistant serves the reply whole at this url as well as streaming it over the API,
		// and this event arrives before the first streamed byte. Fetching it is how announcements
		// already play, and it cannot gap: the streamed copy arrives at about the rate it plays
		// out, so any hiccup empties the queue and silence lands in the middle of a word. HTTP also
		// says when the audio has ended, which the streamed copy does not reliably do.
		url := e.Data["url"]
		if url == "" {
			return
		}

		t.mu.Lock()
		t.replyURL = url
		t.replying = true
		t.mu.Unlock()

		go alog.Safely("reply", func() {
			// The ring and the media player belong to the reply until it has been heard. Home
			// Assistant ends the turn as soon as it has handed the text over, which is before the
			// audio exists, so leaving it to the turn puts the device back to idle while it is
			// still about to speak.
			defer t.replyDone()

			if err := t.play(url); err != nil {
				slog.Error("fetching the reply failed", "url", url, "err", err)
			}
		})

	case api.VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END:
		t.end()

	case api.VoiceAssistantEvent_VOICE_ASSISTANT_ERROR:
		slog.Error("pipeline error", "code", e.Data["code"], "message", e.Data["message"])
		t.end()
		t.trouble()
	}
}

// tts plays a reply Home Assistant streams over the API, as 16 kHz mono. This is the fallback: when
// a url arrives the reply is fetched whole instead, which cannot gap.
func (t *voiceTurn) tts(data []byte, end bool) {
	if len(data) > 0 && t.speaker != nil {
		t.mu.Lock()
		fetching := t.replyURL != ""
		t.mu.Unlock()
		if fetching {
			return
		}

		samples := make([]int16, len(data)/2)
		for i := range samples {
			samples[i] = int16(uint16(data[i*2]) | uint16(data[i*2+1])<<8)
		}
		// How loud the reply arrives decides whether interpolating it can overflow, and the byte
		// count against how long it takes to say is how a wrong sample rate would show up.
		peak := 0
		for _, s := range samples {
			peak = max(peak, int(s), -int(s))
		}

		t.mu.Lock()
		first := t.replyBytes == 0
		t.replyBytes += len(data)
		t.replyPeak = max(t.replyPeak, peak)
		if first {
			t.splicesAt = t.speaker.Splices()
		}
		t.mu.Unlock()

		t.queueReply(samples)

		// Home Assistant ends the turn before the audio for it arrives, so what happens to the
		// reply has to be followed on its own rather than as part of the turn.
		if first {
			go alog.Safely("reply report", t.reportReply)
		}
	}
	if end {
		go alog.Safely("tts playback", t.waitForPlayback)
	}
}

// replyCushion is how much of a reply to hold before starting to play it. The reply arrives over the
// network at roughly the rate it plays, so without a cushion every hiccup empties the queue and
// silence gets spliced into the middle of a word. It is added latency, so it is only as large as it
// needs to be to cover the jitter actually seen.
const replyCushion = 500 * time.Millisecond

// queueReply hands audio to the speaker once enough has arrived to play through a gap. Whenever the
// queue does run empty the cushion is rebuilt, rather than dribbling out what little has arrived.
func (t *voiceTurn) queueReply(samples []int16) {
	cushion := int(replyCushion/time.Millisecond) * speaker.VoiceRate / 1000

	t.mu.Lock()
	t.held = append(t.held, samples...)
	if t.speaker.Queued() == 0 {
		t.flowing = false
	}
	if !t.flowing && len(t.held) < cushion {
		t.mu.Unlock()
		return
	}
	t.flowing = true
	out := t.held
	t.held = nil
	t.mu.Unlock()

	t.speaker.PlayVoice(out)
}

// flushReply plays whatever is still held, for a reply that ended before filling the cushion.
func (t *voiceTurn) flushReply() {
	t.mu.Lock()
	out := t.held
	t.held = nil
	t.mu.Unlock()

	if len(out) > 0 {
		t.speaker.PlayVoice(out)
	}
}

// reportReply follows one reply to the end and says how it went. One seam is the reply running out
// at its end; more than that is silence spliced into the middle of speech, which is what a reply
// arriving over the network slower than it plays out sounds like.
func (t *voiceTurn) reportReply() {
	// A reply shorter than the cushion would otherwise sit there unplayed.
	time.Sleep(replyCushion)
	t.flushReply()

	// Chunks arrive with gaps, so the queue emptying once does not mean the reply is over. Wait for
	// it to stay empty.
	const idleFor = 8
	for idle := 0; idle < idleFor; {
		if t.speaker.Queued() == 0 {
			idle++
		} else {
			idle = 0
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.mu.Lock()
	bytes, peak, at := t.replyBytes, t.replyPeak, t.splicesAt
	t.replyBytes, t.replyPeak = 0, 0
	t.mu.Unlock()

	slog.Info("reply played",
		"bytes", bytes,
		"seconds", float64(bytes)/float64(2*mic.Rate),
		"peak", peak,
		"seams", t.speaker.Splices()-at,
		"clipped", t.speaker.Clipped(),
		"dropped", t.source.Dropped())
}

// waitForPlayback lets the queued reply finish before the turn is declared over, so the ring and
// the media player do not go idle mid-sentence.
func (t *voiceTurn) waitForPlayback() {
	if t.speaker == nil {
		t.end()
		return
	}

	for t.speaker.Queued() > 0 {
		time.Sleep(50 * time.Millisecond)
	}
	t.end()
}

// troubleFlash is how long the ring shows a failure, and troubleColor what it shows. Red is not
// used anywhere else, so it never has to be told apart from an effect or a volume arc.
const troubleFlash = 1500 * time.Millisecond

var troubleColor = led.Color{R: 0xC0, G: 0x00, B: 0x00}

// replyDone puts the ring and the media player back, once the reply has actually been spoken.
func (t *voiceTurn) replyDone() {
	t.mu.Lock()
	t.replying = false
	t.mu.Unlock()

	t.player.mp.SetState(esphome.MediaPlayerIdle)
	t.ring.still()
	t.ring.restore()
}

// trouble says a request could not be served. Home Assistant failing an intent, or not listening at
// all, otherwise looks exactly like the device having ignored the person: the turn simply stops and
// nothing is said.
func (t *voiceTurn) trouble() {
	chime(t.speaker, toneTrouble)

	frame := make([]led.Color, led.Segments)
	for i := range frame {
		frame[i] = troubleColor
	}
	t.ring.Flash(frame, troubleFlash)
}

// sending reports whether microphone audio is still going to Home Assistant. stop is cleared when
// the pipeline says it has heard enough, so this is true for exactly the listening part of a turn.
func (t *voiceTurn) sending() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stop != nil
}

func (t *voiceTurn) stopStreaming() {
	t.mu.Lock()
	stop := t.stop
	t.stop = nil
	t.mu.Unlock()

	if stop != nil {
		stop()
		if err := t.vs.EndAudio(); err != nil {
			slog.Error("ending audio failed", "err", err)
		}

		// The same animation, turned around: the device has stopped listening and is waiting on an
		// answer, which is a different thing to be doing and looks like one.
		if t.effect != nil {
			if e := t.effect(); e != "" {
				t.ring.HoldEffectReversed(e)
			}
		}
	}
}

// end closes the turn whatever happened, and puts the ring and the player back.
func (t *voiceTurn) end() {
	t.stopStreaming()

	t.mu.Lock()
	was := t.running
	t.running = false
	t.mu.Unlock()

	if !was {
		return
	}

	_ = t.vs.StopTurn()

	// A reply already on its way owns the ring and the player until it has been spoken, and puts
	// them back itself.
	t.mu.Lock()
	replying := t.replying
	t.mu.Unlock()
	if !replying {
		t.player.mp.SetState(esphome.MediaPlayerIdle)
		t.ring.still()
		t.ring.restore()
	}
	slog.Info("turn ended", "replying", replying)
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
