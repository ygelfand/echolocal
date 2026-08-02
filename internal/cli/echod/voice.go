package echod

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

func newVoiceCmd() *cobra.Command {
	var volume int

	c := &cobra.Command{
		Use:   "voice <file.wav>",
		Short: "Play a WAV through the voice path",
		Long: "Plays through the same player a Home Assistant reply goes through, including the\n" +
			"mixer setup and the amplifier, so what comes out is what a reply sounds like.\n\n" +
			"16 kHz mono is interpolated up to the codec's 48 kHz, exactly as a reply is. Audio\n" +
			"that is already 48 kHz stereo is queued untouched. Playing the same speech both ways\n" +
			"is how to tell the resampler apart from everything after it: convert it first with\n" +
			"  afconvert -f WAVE -d LEI16@48000 -c 2 reply.wav reply48.wav\n\n" +
			"echod holds the playback device, so stop the service first and start it after:\n" +
			"  setprop ctl.stop ledcontroller\n" +
			"  setprop ctl.start ledcontroller",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pcm, rate, channels, err := readWAVFile(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %d Hz, %d channel(s), %.2f s\n",
				args[0], rate, channels, float64(len(pcm))/float64(rate*channels))

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

			// Let the mixer sequence and the amplifier settle, the same wait a reply gets.
			time.Sleep(1500 * time.Millisecond)

			switch {
			case rate == speaker.VoiceRate && channels == 1:
				fmt.Fprintln(cmd.OutOrStdout(), "queueing through the resampler")
				p.PlayVoice(pcm)
			case rate == speaker.Rate && channels == speaker.Channels:
				fmt.Fprintln(cmd.OutOrStdout(), "queueing as it is")
				p.Play(pcm)
			default:
				return fmt.Errorf("need %d Hz mono or %d Hz stereo, got %d Hz with %d channel(s)",
					speaker.VoiceRate, speaker.Rate, rate, channels)
			}

			for p.Queued() > 0 {
				time.Sleep(50 * time.Millisecond)
			}
			// The tail is still in the hardware buffer.
			time.Sleep(300 * time.Millisecond)

			fmt.Fprintf(cmd.OutOrStdout(), "clipped %d samples, %d seams\n", p.Clipped(), p.Splices())
			return nil
		},
	}

	c.Flags().IntVar(&volume, "volume", -1, "volume step to play at, or -1 to use the saved one")
	return c
}

// readWAVFile reads a 16-bit PCM WAV, walking the chunk list rather than assuming a fixed header.
func readWAVFile(path string) (pcm []int16, rate, channels int, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(raw) < 12 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, 0, 0, fmt.Errorf("%s is not a WAV file", path)
	}

	total := uint64(len(raw))
	for off := uint64(12); off+8 <= total; {
		id := string(raw[off : off+4])
		size := uint64(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		off += 8
		end := min(off+size, total)

		switch id {
		case "fmt ":
			if end-off < 16 {
				return nil, 0, 0, fmt.Errorf("%s has a short format chunk", path)
			}
			channels = int(binary.LittleEndian.Uint16(raw[off+2:]))
			rate = int(binary.LittleEndian.Uint32(raw[off+4:]))
			if bits := binary.LittleEndian.Uint16(raw[off+14:]); bits != 16 {
				return nil, 0, 0, fmt.Errorf("%s is %d-bit, need 16", path, bits)
			}
		case "data":
			pcm = make([]int16, (end-off)/2)
			for i := range pcm {
				pcm[i] = int16(binary.LittleEndian.Uint16(raw[off+uint64(2*i):]))
			}
		}
		off = end + size%2
	}

	if len(pcm) == 0 || rate == 0 || channels == 0 {
		return nil, 0, 0, fmt.Errorf("%s has no audio", path)
	}
	return pcm, rate, channels, nil
}
