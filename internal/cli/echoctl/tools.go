package echoctl

import (
	"fmt"
	"math"
	"os"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/audio"
)

func newToolsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "tools",
		Short: "Host-side audio tools",
	}
	c.AddCommand(newToneCmd(), newWavCmd(), newAnalyzeCmd(), newDirectionCmd())
	return c
}

func newAnalyzeCmd() *cobra.Command {
	var (
		in     string
		chans  int
		maxLag int
	)

	c := &cobra.Command{
		Use:   "analyze",
		Short: "Cross-correlate the channels of a raw capture",
		Long: "Answers two questions about the mic array from a single silent-room recording.\n\n" +
			"Is any channel a hardware mix rather than its own microphone? Independent mics\n" +
			"and preamps have independent noise, so in silence they correlate near zero. A\n" +
			"channel summed from others correlates strongly with all of them.\n\n" +
			"Which channel is the center mic? A true center correlates about equally with\n" +
			"every perimeter mic; a perimeter mic favours its neighbours.\n\n" +
			"ch7/ch8 are the playback loopback, so they read as zero when nothing is playing.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if in == "" {
				return fmt.Errorf("need --in")
			}
			raw, err := os.ReadFile(in)
			if err != nil {
				return err
			}

			ch := audio.Channels(raw, chans)
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%d channels, %d frames\n", chans, len(ch[0]))

			fmt.Fprintln(w, "\n=== correlation matrix (zero lag) ===")
			audio.CorrelationMatrix(w, ch)

			fmt.Fprintln(w, "\n=== mean |correlation| with other channels ===")
			fmt.Fprintln(w, "an outlier here is a candidate hardware mix")
			for i := range ch {
				fmt.Fprintf(w, "ch%d  %.3f\n", i, audio.MeanAbsCorrelation(ch, i))
			}

			if chans >= 7 {
				t := audio.AnalyzeTopology(ch, []int{0, 1, 2, 3, 4, 5}, 6)
				fmt.Fprintln(w, "\n=== topology (assumes ring 0-5, center 6) ===")
				fmt.Fprintf(w, "center ch6 spread:  %.4f\n", t.CenterSpread)
				for i, s := range t.Spreads {
					fmt.Fprintf(w, "perimeter ch%d spread: %.4f\n", i, s)
				}
				fmt.Fprintf(w, "\nmean correlation by ring distance (36mm / 62mm / 72mm apart):\n")
				fmt.Fprintf(w, "  d=1 adjacent %.4f\n  d=2 two-apart %.4f\n  d=3 opposite  %.4f\n",
					t.ByDistance[1], t.ByDistance[2], t.ByDistance[3])
				if t.Monotonic() {
					fmt.Fprintln(w, "  -> monotonic with separation: consistent with this ring order")
				} else {
					fmt.Fprintln(w, "  -> NOT monotonic: ring order is wrong or no coherent field")
				}
			}

			fmt.Fprintf(w, "\n=== best lag vs ch0, +/-%d samples ===\n", maxLag)
			for i := range ch {
				lag, v := audio.BestLag(ch[i], ch[0], maxLag)
				fmt.Fprintf(w, "ch%d  lag %+d  r=%.3f\n", i, lag, v)
			}
			return nil
		},
	}

	c.Flags().StringVar(&in, "in", "", "raw interleaved S24_3LE capture")
	c.Flags().IntVarP(&chans, "channels", "c", 9, "channels in the input")
	c.Flags().IntVar(&maxLag, "max-lag", 8, "cross-correlation search range in samples")
	return c
}

func newDirectionCmd() *cobra.Command {
	var (
		in     string
		chans  int
		rate   int
		window int
	)

	c := &cobra.Command{
		Use:   "direction",
		Short: "Estimate which mic faced the source in a directional capture",
		Long: "For a recording made with a sound source at one known bearing, reports each\n" +
			"perimeter mic's level and its arrival time relative to the center mic (ch6).\n\n" +
			"The mic facing the source should be loudest and earliest. With a ring radius of\n" +
			"36 mm the expected lag range is +/-1.7 samples at 16 kHz, so lags are measured to\n" +
			"sub-sample precision; a negative lag means that mic heard it before the center.\n\n" +
			"Repeat at a few known bearings to pin the array's absolute rotation.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if in == "" {
				return fmt.Errorf("need --in")
			}
			raw, err := os.ReadFile(in)
			if err != nil {
				return err
			}
			ch := audio.Channels(raw, chans)
			w := cmd.OutOrStdout()

			if window > 0 {
				start := audio.LoudestWindow(ch[6], window)
				fmt.Fprintf(w, "analysing loudest %d samples (%.2fs) starting at %.2fs\n",
					window, float64(window)/float64(rate), float64(start)/float64(rate))
				ch = audio.SliceAll(ch, start, window)
			}

			var st audio.Stats
			for _, v := range ch[6] {
				st.Add(int32(v))
			}
			fmt.Fprintf(w, "center ch6 rms: %.1f dBFS\n\n", audio.DBFS(st.RMS()))

			fmt.Fprintf(w, "%4s  %10s  %12s  %12s  %8s\n",
				"mic", "rms dBFS", "rel dB", "lag samples", "path mm")

			var levels []float64
			for i := 0; i < 6; i++ {
				var s audio.Stats
				for _, v := range ch[i] {
					s.Add(int32(v))
				}
				levels = append(levels, audio.DBFS(s.RMS()))
			}
			mean := 0.0
			for _, l := range levels {
				mean += l
			}
			mean /= float64(len(levels))

			offsets := make([]float64, 6)
			for i := 0; i < 6; i++ {
				lag, _ := audio.ArrivalOffset(ch[i], ch[6], 4)
				offsets[i] = lag
				fmt.Fprintf(w, "%4d  %10.1f  %+12.2f  %+12.2f  %+8.1f\n",
					i, levels[i], levels[i]-mean, lag, audio.LagToDistance(lag, rate))
			}

			bearing, amp := audio.FitBearing(offsets)
			expect := audio.InPlaneLag(36, rate)

			fmt.Fprintf(w, "\nfitted bearing:  %.0f deg from ch0, measuring toward ch1\n", bearing)
			fmt.Fprintf(w, "fit amplitude:   %.2f samples (in-plane would be %.2f)\n", amp, expect)

			if amp < 0.3*expect {
				fmt.Fprintln(w, "-> weak fit: source too diffuse or too far; bearing is unreliable")
			} else if amp < 0.8*expect {
				el := math.Acos(math.Min(1, amp/expect)) * 180 / math.Pi
				fmt.Fprintf(w, "-> consistent with a source about %.0f deg above the ring plane\n", el)
			} else {
				fmt.Fprintln(w, "-> strong in-plane fit")
			}

			// Level should peak toward the same bearing, independently of timing.
			loudest := 0
			for i := range levels {
				if levels[i] > levels[loudest] {
					loudest = i
				}
			}
			byLevel := float64(loudest) * 60
			diff := math.Abs(math.Mod(bearing-byLevel+540, 360) - 180)
			fmt.Fprintf(w, "loudest mic ch%d sits at %.0f deg; level and timing differ by %.0f deg\n",
				loudest, byLevel, diff)
			if diff <= 60 {
				fmt.Fprintln(w, "-> level and timing agree")
			} else {
				fmt.Fprintln(w, "-> level and timing disagree; suspect reflections or a near-field source")
			}
			return nil
		},
	}

	c.Flags().StringVar(&in, "in", "", "raw interleaved S24_3LE capture")
	c.Flags().IntVarP(&chans, "channels", "c", 9, "channels in the input")
	c.Flags().IntVarP(&rate, "rate", "r", 16000, "sample rate")
	c.Flags().IntVar(&window, "window", 4096, "analyse only the loudest N samples; 0 uses all")
	return c
}

func newToneCmd() *cobra.Command {
	var (
		out         string
		freq, freqR float64
		amp, ampR   float64
		secs        float64
		rate, chans int
	)

	c := &cobra.Command{
		Use:   "tone",
		Short: "Generate a sine WAV as a stable acoustic reference",
		Long: "Playback on the Echo Dot accepts 48000 Hz only, so use -r 48000 for anything\n" +
			"destined for the device. Distinct -f2/-a2 make the two channels\n" +
			"distinguishable, which is how the ch7/ch8 loopback was identified.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pcm := audio.Tone(freq, secs, rate, amp)
			if chans == 2 && (freqR > 0 || ampR > 0) {
				rf, ra := freqR, ampR
				if rf == 0 {
					rf = freq
				}
				if ra == 0 {
					ra = amp
				}
				right := audio.Tone(rf, secs, rate, ra)
				wide := make([]byte, 0, len(pcm)*2)
				for i := 0; i+1 < len(pcm) && i+1 < len(right); i += 2 {
					wide = append(wide, pcm[i], pcm[i+1], right[i], right[i+1])
				}
				pcm = wide
			} else if chans > 1 {
				wide := make([]byte, 0, len(pcm)*chans)
				for i := 0; i+1 < len(pcm); i += 2 {
					for ch := 0; ch < chans; ch++ {
						wide = append(wide, pcm[i], pcm[i+1])
					}
				}
				pcm = wide
			}

			if err := audio.WriteWAV(out, pcm, rate, chans); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %.0f Hz, %.1fs, %d Hz, %d ch, amplitude %.3f\n",
				out, freq, secs, rate, chans, amp)
			return nil
		},
	}

	c.Flags().StringVarP(&out, "out", "o", "tone.wav", "output WAV")
	c.Flags().Float64VarP(&freq, "freq", "f", 1000, "frequency in Hz")
	c.Flags().Float64Var(&freqR, "f2", 0, "right-channel frequency; 0 copies the left")
	c.Flags().Float64VarP(&amp, "amp", "a", 0.5, "amplitude, 0..1 of full scale")
	c.Flags().Float64Var(&ampR, "a2", 0, "right-channel amplitude; 0 uses --amp")
	c.Flags().Float64VarP(&secs, "seconds", "t", 10, "duration")
	c.Flags().IntVarP(&rate, "rate", "r", 48000, "sample rate")
	c.Flags().IntVarP(&chans, "channels", "c", 2, "channels")
	return c
}

func newWavCmd() *cobra.Command {
	var (
		in, out string
		chans   int
		ch      int
		gainDB  float64
		rate    int
	)

	c := &cobra.Command{
		Use:   "wav",
		Short: "Convert a raw 9-channel S24_3LE capture to a listenable mono WAV",
		Long: "Input is what `echod tools mic --raw` writes. Channel -1 averages the seven\n" +
			"microphones; 7 and 8 are the playback loopback, not microphones.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if in == "" {
				return fmt.Errorf("need --in")
			}
			data, err := os.ReadFile(in)
			if err != nil {
				return err
			}

			frameBytes := chans * 3
			frames := len(data) / frameBytes
			gain := math.Pow(10, gainDB/20)

			pcm := make([]byte, 0, frames*2)
			var clipped int
			var peak float64

			for f := 0; f < frames; f++ {
				off := f * frameBytes

				var v float64
				if ch >= 0 {
					v = float64(audio.DecodeS24LE3(data[off+ch*3:]))
				} else {
					for c := 0; c < 7; c++ {
						v += float64(audio.DecodeS24LE3(data[off+c*3:]))
					}
					v /= 7
				}

				v *= gain
				if a := math.Abs(v); a > peak {
					peak = a
				}
				if v > audio.FullScale-1 {
					v, clipped = audio.FullScale-1, clipped+1
				} else if v < -audio.FullScale {
					v, clipped = -audio.FullScale, clipped+1
				}

				s := int16(int32(v) >> 8)
				pcm = append(pcm, byte(s), byte(s>>8))
			}

			if err := audio.WriteWAV(out, pcm, rate, 1); err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%s: %d frames, %.2fs, gain %+.1f dB\n",
				out, frames, float64(frames)/float64(rate), gainDB)
			fmt.Fprintf(w, "peak after gain: %.1f dBFS\n", audio.DBFS(peak))
			if clipped > 0 {
				fmt.Fprintf(w, "CLIPPED: %d samples (%.3f%%)\n",
					clipped, 100*float64(clipped)/float64(frames))
			}
			return nil
		},
	}

	c.Flags().StringVar(&in, "in", "", "raw interleaved S24_3LE input")
	c.Flags().StringVarP(&out, "out", "o", "out.wav", "output WAV")
	c.Flags().IntVarP(&chans, "channels", "c", 9, "channels in the input")
	c.Flags().IntVar(&ch, "ch", -1, "channel to extract; -1 averages the 7 mics")
	c.Flags().Float64VarP(&gainDB, "gain", "g", 0, "gain in dB")
	c.Flags().IntVarP(&rate, "rate", "r", 16000, "sample rate")
	return c
}
