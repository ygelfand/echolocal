package voice

import (
	"context"
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/alog"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

// announce plays what Home Assistant asks for, then reports back. It runs off the connection's
// read loop: fetching and playing must not block it.
func (t *conversation) announce(a esphome.Announce) {
	slog.Info("announce", "text", a.Text, "start_conversation", a.StartConversation)

	// One claim covers both urls, so silencing an announcement stops the whole thing rather than
	// letting the second one start once the first has been drained.
	t.player.Sounding(true)
	claim := t.sound.Claim("announce", func(ctx context.Context, _ *speaker.Player) error {
		for _, url := range []string{a.PreannounceMediaID, a.MediaID} {
			if url == "" {
				continue
			}
			if err := t.play(ctx, url); err != nil {
				return err
			}
		}
		return nil
	})

	go alog.Safely("announce", func() {
		<-claim.Done()
		t.player.Sounding(false)

		if err := claim.Err(); err != nil {
			slog.Error("playing the announcement failed", "err", err)
		}
		if err := t.vs.AnnounceFinished(claim.Err() == nil && !claim.Stopped()); err != nil {
			slog.Error("reporting the announcement failed", "err", err)
		}

		// Home Assistant uses this to open a conversation without a wake word. An announcement that
		// was cut off was cut off on purpose, so nothing follows it.
		if a.StartConversation && !claim.Stopped() {
			t.Start(0)
		}
	})
}

// play fetches audio and queues it. Home Assistant serves it converted to media.Formats. Waiting
// for it to be heard is the driver's, not ours: this returns as soon as it has been handed over.
func (t *conversation) play(ctx context.Context, url string) error {
	samples, err := media.Fetch(ctx, url)
	if err != nil {
		return err
	}
	slog.Info("playing announcement", "samples", len(samples))

	t.post(event{kind: evPlaying})
	t.speaker.PlayVoice(samples)
	t.speaker.PlayVoice(make([]int16, speaker.VoiceRate*media.Tail/1000))
	return nil
}
