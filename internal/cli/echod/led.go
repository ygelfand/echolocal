package echod

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/led"
)

func newLEDCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "led",
		Short: "Drive the LED ring directly",
		Long: "Writes the is31fl3236 driver's frame attribute, bypassing Amazon's LedController.\n" +
			"This works even when ledctrl brightness is 0, and survives de-Amazoning.",
	}

	var (
		path    string
		delayMS int
	)
	c.PersistentFlags().StringVar(&path, "path", led.DefaultPath, "is31fl3236 sysfs directory")

	ring := func() *led.Ring { return &led.Ring{Path: path} }

	fill := &cobra.Command{
		Use:   "fill <0-255>",
		Short: "Set every channel to one brightness",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var v int
			if _, err := fmt.Sscanf(args[0], "%v", &v); err != nil {
				return err
			}
			if v < 0 || v > 255 {
				return fmt.Errorf("brightness must be 0-255, got %d", v)
			}
			return ring().Fill(byte(v))
		},
	}

	off := &cobra.Command{
		Use:   "off",
		Short: "Blank the ring",
		RunE:  func(*cobra.Command, []string) error { return ring().Off() },
	}

	set := &cobra.Command{
		Use:   "set <72-hex-chars>",
		Short: "Write a raw 36-byte frame",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			b, err := hex.DecodeString(strings.TrimSpace(args[0]))
			if err != nil {
				return err
			}
			return ring().SetFrame(b)
		},
	}

	walk := &cobra.Command{
		Use:   "walk",
		Short: "Light one channel at a time to map channels to physical positions",
		Long: "Prints the channel index and waits for Enter before advancing, so the mapping\n" +
			"from the 36 channels to ring position and RGB order can be written down.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := ring()
			for i := 0; i < led.Channels; i++ {
				buf := make([]byte, led.Channels)
				buf[i] = 0xff
				if err := r.SetFrame(buf); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "channel %d lit - press Enter for next\n", i)
				var discard string
				_, _ = fmt.Fscanln(cmd.InOrStdin(), &discard)
			}
			return r.Off()
		},
	}

	parseColor := func(s string) (led.Color, error) {
		b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(s), "#"))
		if err != nil || len(b) != 3 {
			return led.Color{}, fmt.Errorf("color must be 6 hex digits like ff8000, got %q", s)
		}
		return led.Color{R: b[0], G: b[1], B: b[2]}, nil
	}

	seg := &cobra.Command{
		Use:   "seg <0-11> <rrggbb>",
		Short: "Light one ring segment and blank the rest",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			var n int
			if _, err := fmt.Sscanf(args[0], "%d", &n); err != nil {
				return err
			}
			col, err := parseColor(args[1])
			if err != nil {
				return err
			}
			return ring().SetSegment(n, col)
		},
	}

	all := &cobra.Command{
		Use:   "all <rrggbb>",
		Short: "Paint every segment one color",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			col, err := parseColor(args[0])
			if err != nil {
				return err
			}
			return ring().SetAll(col)
		},
	}

	segwalk := &cobra.Command{
		Use:   "segwalk",
		Short: "Step through the 12 segments to establish ring direction",
		Long: "Lights each segment in turn with a delay, so the direction of increasing segment\n" +
			"index around the ring can be read off. Segment 0 is at the bottom-right, between\n" +
			"the microphone and volume-down buttons.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := ring()
			for i := 0; i < led.Segments; i++ {
				if err := r.SetSegment(i, led.Color{R: 0xff, G: 0xff, B: 0xff}); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "segment %d\n", i)
				time.Sleep(time.Duration(delayMS) * time.Millisecond)
			}
			return r.Off()
		},
	}
	segwalk.Flags().IntVar(&delayMS, "delay", 1200, "milliseconds per segment")

	current := &cobra.Command{
		Use:   "current [value]",
		Short: "Get or set the global drive current",
		Long: "With no argument, prints the current value. This gates the whole ring: at 0 a\n" +
			"frame write is accepted and reads back correctly but nothing lights up.\n" +
			"`ledctrl -c` resets it to 0, so check here before suspecting dead hardware.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := ring()
			if len(args) == 0 {
				v, err := r.Current()
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), v)
				return nil
			}
			var v int
			if _, err := fmt.Sscanf(args[0], "%d", &v); err != nil {
				return err
			}
			return r.SetCurrent(v)
		},
	}

	show := &cobra.Command{
		Use:   "show",
		Short: "Print the current frame and driver settings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := ring()
			f, err := r.Frame()
			if err != nil {
				return err
			}
			cur, err := r.Current()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "frame:       %s\nled_current: %d\n",
				hex.EncodeToString(f), cur)
			return nil
		},
	}

	c.AddCommand(fill, off, set, walk, segwalk, seg, all, show, current)
	return c
}
