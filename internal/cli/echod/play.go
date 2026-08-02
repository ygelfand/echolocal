package echod

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/lib/alsa"
)

// The playback codec accepts one format only.
const (
	playRate     = 48000
	playChannels = 2
	playBits     = 16
	playPeriod   = 1024
	playPeriods  = 4
)

func newPlayCmd() *cobra.Command {
	var (
		card    int
		device  int
		freq    float64
		secs    float64
		level   float64
		silence bool
	)

	c := &cobra.Command{
		Use:   "play",
		Short: "Play a test tone through the speaker",
		Long: "Writes to the playback PCM directly. The codec accepts S16_LE, 48 kHz, stereo and\n" +
			"nothing else.\n\n" +
			"Nothing is audible while the speaker amp switches are off — see `tools mixer`. Use\n" +
			"--silence to exercise the path without making noise, which is also how to tell a\n" +
			"broken stream from a muted one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := alsa.OpenPlayback(card, device, alsa.Config{
				Channels:   playChannels,
				Rate:       playRate,
				Format:     alsa.FormatS16_LE,
				Bits:       playBits,
				PeriodSize: playPeriod,
				Periods:    playPeriods,
			})
			if err != nil {
				return err
			}
			defer p.Close()

			out := cmd.OutOrStdout()
			what := fmt.Sprintf("%.0f Hz at %.0f%%", freq, level*100)
			if silence {
				what = "silence"
			}
			fmt.Fprintf(out, "playing %s for %.1fs on card %d device %d\n", what, secs, card, device)

			frames := int(secs * playRate)
			buf := make([]byte, playPeriod*playChannels*playBits/8)
			start := time.Now()

			for done := 0; done < frames; {
				n := min(playPeriod, frames-done)
				fill(buf[:n*playChannels*playBits/8], done, freq, level, silence)

				if _, err := p.Write(buf[:n*playChannels*playBits/8]); err != nil {
					if err == alsa.ErrUnderrun {
						fmt.Fprintln(out, "underrun")
						continue
					}
					return err
				}
				done += n
			}

			if err := p.Drain(); err != nil {
				return fmt.Errorf("drain: %w", err)
			}
			fmt.Fprintf(out, "wrote %d frames in %s\n", frames, time.Since(start).Round(time.Millisecond))
			return nil
		},
	}

	c.Flags().IntVar(&card, "card", 0, "sound card index")
	c.Flags().IntVar(&device, "device", 23, "playback PCM device")
	c.Flags().Float64Var(&freq, "freq", 440, "tone frequency in Hz")
	c.Flags().Float64Var(&secs, "seconds", 1, "how long to play")
	c.Flags().Float64Var(&level, "level", 0.2, "amplitude, 0 to 1")
	c.Flags().BoolVar(&silence, "silence", false, "write zeros instead of a tone")
	return c
}

// fill writes one period of interleaved stereo, continuing the tone from frame offset so
// periods join without a click.
func fill(buf []byte, offset int, freq, level float64, silence bool) {
	for i := range len(buf) / (playChannels * playBits / 8) {
		var s int16
		if !silence {
			t := float64(offset+i) / playRate
			s = int16(level * math.MaxInt16 * math.Sin(2*math.Pi*freq*t))
		}
		for ch := range playChannels {
			binary.LittleEndian.PutUint16(buf[(i*playChannels+ch)*2:], uint16(s))
		}
	}
}
