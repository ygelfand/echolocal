package echod

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/zserge/microwakeword"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/wake"
)

func newWakeCmd() *cobra.Command {
	var (
		dir     string
		secs    float64
		wav     string
		gain    float64
		compare bool
	)

	c := &cobra.Command{
		Use:   "wake",
		Short: "Listen for a wake word and report detections",
		Long: "Runs microWakeWord over the microphone array and prints every detection with the\n" +
			"sliding-window score, plus how long inference takes per frame.\n\n" +
			"The model is a .tflite file on the device. Nothing is sent anywhere.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			models, err := wake.Installed(dir)
			if err != nil {
				return err
			}

			// Same selection the agent uses: Home Assistant's, via the saved config.
			m := wake.Pick(models, config.Get().Wake.Slot(0).ID)

			det, err := microwakeword.NewDetector(m.Config)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			if wav != "" {
				return wakeFromWAV(out, det, wav)
			}

			source, err := mic.Acquire()
			if err != nil {
				return err
			}
			defer source.Close()

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			go func() {
				if err := source.Run(ctx); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), "capture stopped:", err)
				}
			}()

			if compare {
				return compareMixes(ctx, out, source, m, int(secs*1000)/20)
			}

			frames, unlisten := source.Listen("tools wake")
			defer unlisten()

			fmt.Fprintf(out, "listening on the center mic for %.0fs: %q (%s), cutoff %.2f, window %d, step %dms\n",
				secs, m.Phrase, m.ID, m.Config.ProbabilityCutoff, m.Config.SlidingWindowSize, m.Config.FeaturesStepMs)

			deadline := time.Now().Add(time.Duration(secs * float64(time.Second)))
			var (
				processed int
				spent     time.Duration
				worst     time.Duration
				hits      int
				best      float64
				loudest   = math.Inf(-1)
				clipped   int
			)

			for time.Now().Before(deadline) {
				select {
				case <-ctx.Done():
					return nil
				case frame, ok := <-frames:
					if !ok {
						return nil
					}
					if gain != 1 {
						frame = amplify(frame, gain)
					}

					start := time.Now()
					hit := det.ProcessAudio(frame)
					took := time.Since(start)

					// A miss is invisible otherwise, and the interesting question is how close it
					// came, so report the peak score and level between hits.
					if s := det.SlidingAverage(); s > best {
						best = s
					}
					if lvl := peak(frame); lvl > loudest {
						loudest = lvl
					}
					clipped += clipping(frame)
					if processed%50 == 0 {
						fmt.Fprintf(out, "  best score %.3f, loudest %6.1f dBFS, clipped %d samples\n",
							best, loudest, clipped)
						best, loudest, clipped = 0, math.Inf(-1), 0
					}

					processed++
					spent += took
					if took > worst {
						worst = took
					}
					if hit {
						hits++
						fmt.Fprintf(out, "  DETECTED  score %.3f  (%d)\n", det.SlidingAverage(), hits)
					}
				}
			}

			if processed == 0 {
				return fmt.Errorf("no frames arrived")
			}

			// Frames are 20 ms of audio, so the real-time budget per frame is 20 ms.
			avg := spent / time.Duration(processed)
			fmt.Fprintf(out, "\n%d frames, %d detections\n", processed, hits)
			fmt.Fprintf(out, "inference: avg %s, worst %s, %.1f%% of real time\n",
				avg.Round(time.Microsecond), worst.Round(time.Microsecond),
				100*float64(avg)/float64(20*time.Millisecond))
			return nil
		},
	}

	c.Flags().StringVar(&dir, "models", layout.ModelDir, "directory of installed models")
	c.Flags().Float64Var(&secs, "seconds", 30, "how long to listen")
	c.Flags().Float64Var(&gain, "gain", 1, "multiply the microphone before detection, to test level")
	c.Flags().BoolVar(&compare, "compare", false, "score several channel mixes on the same audio")
	c.Flags().StringVar(&wav, "wav", "", "run against a 16 kHz mono WAV instead of the microphones")
	return c
}

// wakeFromWAV checks the model and the interpreter without the microphones in the way.
func wakeFromWAV(out io.Writer, det *microwakeword.Detector, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) <= wavHeader {
		return fmt.Errorf("%s is too short to hold audio", path)
	}

	pcm := data[wavHeader:]
	samples := make([]int16, len(pcm)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(pcm[i*2:]))
	}
	fmt.Fprintf(out, "%s: %d samples, %.2fs at 16 kHz\n", path, len(samples), float64(len(samples))/mic.Rate)

	var (
		hits  int
		spent time.Duration
	)
	for off := 0; off+mic.FrameSamples <= len(samples); off += mic.FrameSamples {
		start := time.Now()
		hit := det.ProcessAudio(samples[off : off+mic.FrameSamples])
		spent += time.Since(start)

		if hit {
			hits++
			fmt.Fprintf(out, "  DETECTED at %.2fs  score %.3f\n", float64(off)/mic.Rate, det.SlidingAverage())
		}
	}

	frames := len(samples) / mic.FrameSamples
	fmt.Fprintf(out, "%d frames, %d detections, avg inference %s per 20ms frame\n",
		frames, hits, (spent / time.Duration(frames)).Round(time.Microsecond))
	return nil
}

// wavHeader is the size of a canonical RIFF/WAVE header.
const wavHeader = 44

// amplify scales a frame, clipping rather than wrapping.
func amplify(frame []int16, gain float64) []int16 {
	out := make([]int16, len(frame))
	for i, s := range frame {
		v := float64(s) * gain
		out[i] = int16(math.Max(math.MinInt16, math.Min(math.MaxInt16, v)))
	}
	return out
}

// clipping counts samples at or near full scale, which is what loud speech close to the array
// looks like when the codec's 20 dB of microphone gain runs out of headroom.
func clipping(frame []int16) int {
	const ceiling = 32000

	var n int
	for _, s := range frame {
		if s >= ceiling || s <= -ceiling {
			n++
		}
	}
	return n
}

func peak(frame []int16) float64 {
	var top float64
	for _, s := range frame {
		if a := math.Abs(float64(s)); a > top {
			top = a
		}
	}
	if top == 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(top/math.MaxInt16)
}
