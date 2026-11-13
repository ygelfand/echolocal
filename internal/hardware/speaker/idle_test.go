package speaker

import (
	"testing"
	"time"

	"github.com/ygelfand/echolocal/internal/config"
)

var start = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func TestTheAmpStaysOnThroughAShortGap(t *testing.T) {
	var g ampIdle
	g.next(false, true, start)

	if got := g.next(false, true, start.Add(idleAfter-time.Millisecond)); got != ampKeep {
		t.Errorf("a gap just short of %v switched the amp: %v", idleAfter, got)
	}
}

func TestTheAmpGoesOffAfterTheIdleTime(t *testing.T) {
	var g ampIdle
	g.next(false, true, start)

	if got := g.next(false, true, start.Add(idleAfter)); got != ampOff {
		t.Errorf("after %v of silence got %v, want off", idleAfter, got)
	}
}

func TestAnOffAmpIsNotTurnedOffAgain(t *testing.T) {
	var g ampIdle
	g.next(false, false, start)

	if got := g.next(false, false, start.Add(time.Hour)); got != ampKeep {
		t.Errorf("an amp already off was switched again: %v", got)
	}
}

func TestAudioTurnsAnOffAmpOn(t *testing.T) {
	var g ampIdle
	if got := g.next(true, false, start); got != ampOn {
		t.Errorf("audio arrived with the amp off and got %v, want on", got)
	}
}

func TestAudioLeavesAnOnAmpAlone(t *testing.T) {
	var g ampIdle
	if got := g.next(true, true, start); got != ampKeep {
		t.Errorf("audio with the amp on switched it: %v", got)
	}
}

func TestAudioRestartsTheIdleClock(t *testing.T) {
	var g ampIdle
	g.next(false, true, start)
	g.next(true, true, start.Add(idleAfter/2))
	g.next(false, true, start.Add(idleAfter))

	if got := g.next(false, true, start.Add(idleAfter*3/2)); got != ampKeep {
		t.Errorf("the clock ran from the first silence, not the last audio: %v", got)
	}
	if got := g.next(false, true, start.Add(idleAfter*2)); got != ampOff {
		t.Errorf("a full idle time after the audio got %v, want off", got)
	}
}

func TestFillReportsSilence(t *testing.T) {
	p := newTestPlayer()
	if p.fill(make([]byte, period*Channels*2)) {
		t.Error("an empty bus was reported as playing")
	}
}

func TestFillReportsQueuedAudio(t *testing.T) {
	p := newTestPlayer()
	p.bus.Push(config.StreamMedia, make([]int16, period*Channels))
	if !p.fill(make([]byte, period*Channels*2)) {
		t.Error("queued audio was reported as silence")
	}
}

func TestFillReportsAnAttachedSource(t *testing.T) {
	p := newTestPlayer()
	p.Attach(&recorder{})
	if !p.fill(make([]byte, period*Channels*2)) {
		t.Error("an attached source was reported as silence")
	}
}

func TestGateDoesNothingWhereTheBoardDoesNotWantIt(t *testing.T) {
	p := newTestPlayer()
	p.ampOn = true
	p.idle.since = start

	p.gate(false)

	if !p.ampOn {
		t.Error("a board without idleOff had its amp switched off")
	}
}

func TestGateTurnsTheAmpOffWhenIdle(t *testing.T) {
	p := newTestPlayer()
	p.seq.idleOff = true
	p.ampOn = true
	p.idle.since = time.Now().Add(-idleAfter)

	p.gate(false)

	if p.ampOn {
		t.Error("the amp stayed on after the idle time")
	}
}

func TestGateTurnsTheAmpOnForAudio(t *testing.T) {
	p := newTestPlayer()
	p.seq.idleOff = true

	p.gate(true)

	if !p.ampOn {
		t.Error("audio arrived and the amp stayed off")
	}
}
