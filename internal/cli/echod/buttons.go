package echod

import (
	"fmt"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/lib/input"
)

func newButtonsCmd() *cobra.Command {
	var secs float64

	c := &cobra.Command{
		Use:   "buttons",
		Short: "Watch the physical buttons and report evdev events",
		Long: "Reads /dev/input/event* directly. Press each button in turn to learn its code.\n\n" +
			"The Echo Dot has four top buttons — action, microphone mute, volume up and volume\n" +
			"down. Whether mute is a real hardware gate on the ADC or only a key event is the\n" +
			"thing worth establishing: check with `tools mic` while muted.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			devs, err := input.List()
			if err != nil {
				return err
			}
			if len(devs) == 0 {
				return fmt.Errorf("no /dev/input/event* nodes")
			}

			out := cmd.OutOrStdout()
			for _, d := range devs {
				fmt.Fprintf(out, "watching %s (%s)\n", d.Path, d.Name)
			}
			fmt.Fprintf(out, "\npress buttons for %.0fs...\n\n", secs)

			var mu sync.Mutex
			done := make(chan struct{})

			for _, d := range devs {
				go func(d *input.Device) {
					for {
						e, err := d.Read()
						if err != nil {
							return
						}
						// SYN just terminates each event group; it carries no information here.
						if e.Type == input.EvSyn {
							continue
						}
						select {
						case <-done:
							return
						default:
						}
						mu.Lock()
						fmt.Fprintf(out, "%-14s %s\n", d.Name, e)
						mu.Unlock()
					}
				}(d)
			}

			time.Sleep(time.Duration(secs * float64(time.Second)))
			close(done)
			for _, d := range devs {
				_ = d.Close()
			}
			return nil
		},
	}

	c.Flags().Float64VarP(&secs, "seconds", "t", 20, "how long to watch")
	return c
}
