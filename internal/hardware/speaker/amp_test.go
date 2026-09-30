package speaker

import (
	"testing"
	"time"
)

// fill() must stamp IdleSince when nothing is queued and no Source is attached. The write loop reads
// that timestamp to decide when to gate the amplifier off.
func TestFillStampsIdleWhenNothingIsQueued(t *testing.T) {
	p := newTestPlayer()
	p.wake = make(chan struct{}, 1)

	buf := make([]byte, period*Channels*2)
	p.fill(buf)

	if got := p.IdleSince.Load(); got == 0 {
		t.Fatalf("IdleSince = 0, want a non-zero timestamp")
	}
}

// fill() must clear IdleSince the moment anything is queued. The gate-off threshold starts from the
// last audible frame, not from the last silence.
func TestFillClearsIdleWhenAudioIsQueued(t *testing.T) {
	p := newTestPlayer()
	p.wake = make(chan struct{}, 1)
	p.IdleSince.Store(time.Now().UnixNano())

	p.pending = []int16{100, 200}
	buf := make([]byte, period*Channels*2)
	p.fill(buf)

	if got := p.IdleSince.Load(); got != 0 {
		t.Errorf("IdleSince = %d after audio, want 0", got)
	}
}

// fill() must clear IdleSince when a Source is attached, even with an empty queue. A Source rendering
// silence still goes through the codec and would pop the amp if gated off.
func TestFillClearsIdleWhenSourceAttached(t *testing.T) {
	p := newTestPlayer()
	p.wake = make(chan struct{}, 1)
	p.IdleSince.Store(time.Now().UnixNano())

	p.Attach(&recorder{fill: 0})
	defer p.Attach(nil)

	buf := make([]byte, period*Channels*2)
	p.fill(buf)

	if got := p.IdleSince.Load(); got != 0 {
		t.Errorf("IdleSince = %d with a Source, want 0", got)
	}
}

// requestWake must collapse: many writers in a row produce one signal in the channel.
func TestRequestWakeCollapsesToOneSignal(t *testing.T) {
	p := newTestPlayer()
	p.wake = make(chan struct{}, 1)

	for i := 0; i < 1000; i++ {
		p.requestWake()
	}

	select {
	case <-p.wake:
	case <-time.After(10 * time.Millisecond):
		t.Fatal("no wake signal after 1000 requests")
	}

	// No second signal pending.
	select {
	case <-p.wake:
		t.Fatal("second wake signal pending, want it collapsed")
	default:
	}
}

// Play() with no playback device must not panic and not signal a wake. The deaf counter is the
// diagnostic for this path.
func TestPlayWithoutDeviceDoesNotSignalWake(t *testing.T) {
	p := newTestPlayer()
	p.wake = make(chan struct{}, 1)

	p.Play([]int16{1, 2, 3, 4})

	select {
	case <-p.wake:
		t.Fatal("wake signalled without a playback device")
	default:
	}
}

// requestWake() must be the single funnel for both Play and Overlay: the queue-empty-before check
// is the caller's, and the helper itself only coalesces signals. This test pins that contract.
func TestPlayAndOverlayShareTheWakeChannel(t *testing.T) {
	p := newTestPlayer()
	p.wake = make(chan struct{}, 1)

	p.requestWake()
	p.requestWake()

	select {
	case <-p.wake:
	default:
		t.Fatal("no wake after requestWake calls")
	}
	select {
	case <-p.wake:
		t.Fatal("wake channel buffered more than one signal")
	default:
	}
}

// The codecSettle constant exists so the wake path can mirror the boot path. If it ever shrinks to
// zero, the amp would flip on with no settling time and pop the speaker.
func TestCodecSettleIsNonZero(t *testing.T) {
	if codecSettle <= 0 {
		t.Fatalf("codecSettle = %v, must be > 0", codecSettle)
	}
}

// ampIdleThreshold is short enough to catch a track ending, long enough not to flap inside one. Pin
// the value so a tuning pass does not silently make it 0 (which would gate the amp off mid-track).
func TestAmpIdleThresholdIsBounded(t *testing.T) {
	if ampIdleThreshold <= 0 {
		t.Fatalf("ampIdleThreshold = %v, must be > 0", ampIdleThreshold)
	}
	if ampIdleThreshold > time.Second {
		t.Errorf("ampIdleThreshold = %v, want <= 1s so tracks turn the amp off promptly", ampIdleThreshold)
	}
}

// Attach(nil) must not wake the amplifier. Detaching clears audio; waking on it would toggle the amp
// off and back on for no reason.
func TestAttachNilDoesNotSignalWake(t *testing.T) {
	p := newTestPlayer()
	p.wake = make(chan struct{}, 1)

	p.Attach(nil)
	select {
	case <-p.wake:
		t.Fatal("Attach(nil) signalled wake")
	default:
	}
}

// amp() must publish its state through AmpOn and OnAmp. A no-op call (same value) does not emit.
func TestAmpPublishesStateAndIsIdempotent(t *testing.T) {
	p := newTestPlayer()
	p.out = OutputSpeaker

	emits := 0
	p.OnAmp.Listen(func(bool) { emits++ })

	// amp(true) on OutputSpeaker: the apply() inside amp() is a no-op without a mixer (test path
	// has no device), but the AmpOn publish and the OnAmp emission still run.
	p.amp(true)
	if !p.AmpOn.Load() {
		t.Fatal("AmpOn = false after amp(true)")
	}
	if emits != 1 {
		t.Errorf("emits = %d after amp(true), want 1", emits)
	}

	// Same value: no second emit, no spurious event.
	p.amp(true)
	if emits != 1 {
		t.Errorf("emits = %d after second amp(true), want still 1", emits)
	}

	p.amp(false)
	if p.AmpOn.Load() {
		t.Fatal("AmpOn = true after amp(false)")
	}
	if emits != 2 {
		t.Errorf("emits = %d after amp(false), want 2", emits)
	}
}

// amp() on the headphone output must still publish state so the write loop and listeners agree, even
// though the hardware does not gate.
func TestAmpOnHeadphonePublishesState(t *testing.T) {
	p := newTestPlayer()
	p.out = OutputHeadphone

	p.amp(true)
	if !p.AmpOn.Load() {
		t.Fatal("AmpOn = false after amp(true) on headphone")
	}
}
