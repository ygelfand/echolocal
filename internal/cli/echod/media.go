package echod

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

func newMediaCmd() *cobra.Command {
	var (
		volume int
		duck   time.Duration
		hold   time.Duration
	)

	c := &cobra.Command{
		Use:   "media <url>",
		Short: "Stream audio from a url through the media path",
		Long: "Plays a url through the same player Home Assistant's media_player uses, which\n" +
			"streams rather than downloading and expects 48 kHz stereo 16-bit WAV — what the\n" +
			"ffmpeg proxy is asked to convert to. Serve one with:\n" +
			"  afconvert -f WAVE -d LEI16@48000 -c 2 track.wav track48.wav\n" +
			"  python3 -m http.server 8000\n\n" +
			"--duck takes the speaker away part way through, the way a voice turn does, and\n" +
			"gives it back after --hold: playing should carry on from where it stopped rather\n" +
			"than skipping ahead.\n\n" +
			"echod holds the playback device, so stop the service first and start it after:\n" +
			"  setprop ctl.stop ledcontroller\n" +
			"  setprop ctl.start ledcontroller",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := speaker.Acquire()
			if err != nil {
				return err
			}
			defer p.Close()

			if volume >= 0 {
				p.SetVolume(volume)
			}

			ctx, stop := context.WithCancel(cmd.Context())
			defer stop()
			go func() { _ = p.Run(ctx) }()

			// The same wait a reply gets, for the mixer sequence and the amplifier.
			time.Sleep(1500 * time.Millisecond)

			out := cmd.OutOrStdout()
			sound := speaker.NewDriver(p)
			player := media.NewStream(sound, p, func() {})

			player.Play(args[0])

			if duck > 0 {
				go func() {
					time.Sleep(duck)
					fmt.Fprintf(out, "taking the speaker for %s\n", hold)
					player.Suspend()

					time.Sleep(hold)
					fmt.Fprintln(out, "giving it back")
					player.Resume()
				}()
			}

			for {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(500 * time.Millisecond):
				}

				playing, paused := player.Playing()
				if !playing && !paused {
					break
				}
				fmt.Fprintf(out, "queued %5d frames, %d seams\n", p.Queued(), p.Splices())
			}

			time.Sleep(speaker.HardwareTail)
			fmt.Fprintf(out, "finished, %d seams\n", p.Splices())
			return nil
		},
	}

	c.Flags().IntVar(&volume, "volume", -1, "volume step to play at, or -1 to use the saved one")
	c.Flags().DurationVar(&duck, "duck", 0, "take the speaker away this far in, as a voice turn does")
	c.Flags().DurationVar(&hold, "hold", 5*time.Second, "how long to keep it")
	return c
}
