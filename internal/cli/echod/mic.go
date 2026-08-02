package echod

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/lib/alsa"
	"github.com/ygelfand/echolocal/internal/lib/audio"
)

func newMicCmd() *cobra.Command {
	var (
		card, device    int
		chans, rate     int
		period, periods int
		secs            float64
		rawPath         string
	)

	c := &cobra.Command{
		Use:   "mic",
		Short: "Capture from the mic array and report per-channel levels",
		Long: "Captures raw audio and reports peak, RMS, and the margin over what a 24->16 bit\n" +
			"truncation would discard.\n\n" +
			"ch0-ch6 are microphones. ch7/ch8 are the playback loopback reference: silent\n" +
			"unless audio is playing, and identifiable by using 0% of the low 8 bits.\n\n" +
			"The capture device is held by mediaserver, so run `stop media` first or this\n" +
			"will block rather than fail.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pcm, err := alsa.Open(card, device, alsa.Config{
				Channels:   chans,
				Rate:       rate,
				Format:     alsa.FormatS24_3LE,
				Bits:       24,
				PeriodSize: period,
				Periods:    periods,
			})
			if err != nil {
				return err
			}
			defer func() { _ = pcm.Close() }()

			var raw *os.File
			if rawPath != "" {
				if raw, err = os.Create(rawPath); err != nil {
					return err
				}
				defer func() { _ = raw.Close() }()
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "capturing %.1fs: %d ch @ %d Hz, S24_3LE, period %d x %d\n",
				secs, chans, rate, period, periods)

			st := make([]audio.Stats, chans)
			buf := make([]byte, period*pcm.FrameBytes())
			deadline := time.Now().Add(time.Duration(secs * float64(time.Second)))
			overruns := 0

			for time.Now().Before(deadline) {
				n, err := pcm.Read(buf)
				if err == alsa.ErrOverrun {
					overruns++
					continue
				}
				if err != nil {
					return err
				}
				if raw != nil {
					if _, err := raw.Write(buf[:n]); err != nil {
						return err
					}
				}
				fb := pcm.FrameBytes()
				for off := 0; off+fb <= n; off += fb {
					for ch := 0; ch < chans; ch++ {
						st[ch].Add(audio.DecodeS24LE3(buf[off+ch*3:]))
					}
				}
			}

			if overruns > 0 {
				fmt.Fprintf(out, "overruns: %d\n", overruns)
			}
			audio.WriteReport(out, st)
			return nil
		},
	}

	c.Flags().IntVarP(&card, "card", "D", 0, "ALSA card")
	c.Flags().IntVarP(&device, "device", "d", 24, "ALSA capture device")
	c.Flags().IntVarP(&chans, "channels", "c", 9, "channels (hardware accepts only 9)")
	c.Flags().IntVarP(&rate, "rate", "r", 16000, "sample rate (hardware accepts only 16000)")
	c.Flags().IntVarP(&period, "period", "p", 256, "period size in frames")
	c.Flags().IntVarP(&periods, "periods", "n", 10, "period count")
	c.Flags().Float64VarP(&secs, "seconds", "t", 5, "capture duration")
	c.Flags().StringVar(&rawPath, "raw", "", "also write raw interleaved S24_3LE here")
	return c
}
