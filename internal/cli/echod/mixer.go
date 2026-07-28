package echod

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/alsa"
)

func newMixerCmd() *cobra.Command {
	var card int

	c := &cobra.Command{
		Use:   "mixer [name] [value]",
		Short: "List or set ALSA mixer controls",
		Long: "With no arguments, lists every control. With a name, prints that one. With a name\n" +
			"and a value, sets it — an item name for enumerated controls, a number otherwise.\n\n" +
			"Playback is silent until the speaker amp switches are on, so this is where sound\n" +
			"gets enabled.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := alsa.OpenMixer(card)
			if err != nil {
				return err
			}
			defer m.Close()

			out := cmd.OutOrStdout()
			switch len(args) {
			case 0:
				controls, err := m.Controls()
				if err != nil {
					return err
				}
				for _, ctl := range controls {
					v, err := m.Get(ctl)
					if err != nil {
						return err
					}
					fmt.Fprintf(out, "%3d  %-40s %s\n", ctl.Numid, ctl.Name, describe(ctl, v))
				}
				return nil

			case 1:
				ctl, err := m.Find(args[0])
				if err != nil {
					return err
				}
				v, err := m.Get(ctl)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "%s = %s\n", ctl.Name, describe(ctl, v))
				fmt.Fprintf(out, "type: %s  count: %d\n", kind(ctl.Type), ctl.Count)
				if ctl.Type == alsa.TypeInteger || ctl.Type == alsa.TypeInteger64 {
					fmt.Fprintf(out, "range: %d..%d step %d\n", ctl.Min, ctl.Max, ctl.Step)
				}
				if len(ctl.Items) > 0 {
					fmt.Fprintf(out, "items: %s\n", strings.Join(ctl.Items, ", "))
				}
				return nil
			}

			ctl, err := m.Find(args[0])
			if err != nil {
				return err
			}
			switch fields := strings.Fields(args[1]); {
			case ctl.Type == alsa.TypeEnumerated:
				if err := m.SetEnum(args[0], args[1]); err != nil {
					return err
				}

			// A control with a value per channel takes them all at once, quoted as one argument.
			case len(fields) > 1:
				values := make([]uint32, 0, len(fields))
				for _, f := range fields {
					n, err := strconv.ParseUint(f, 10, 32)
					if err != nil {
						return fmt.Errorf("%q is not a number", f)
					}
					values = append(values, uint32(n))
				}
				if err := m.SetAll(ctl, values); err != nil {
					return err
				}

			default:
				n, err := strconv.ParseUint(args[1], 10, 32)
				if err != nil {
					return fmt.Errorf("%q is not a number", args[1])
				}
				if err := m.Set(ctl, uint32(n)); err != nil {
					return err
				}
			}

			v, err := m.Get(ctl)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%s = %s\n", ctl.Name, describe(ctl, v))
			return nil
		},
	}

	c.Flags().IntVar(&card, "card", 0, "sound card index")
	return c
}

func kind(t uint32) string {
	switch t {
	case alsa.TypeBoolean:
		return "boolean"
	case alsa.TypeInteger:
		return "integer"
	case alsa.TypeEnumerated:
		return "enumerated"
	case alsa.TypeBytes:
		return "bytes"
	case alsa.TypeInteger64:
		return "integer64"
	}
	return fmt.Sprintf("type %d", t)
}

func describe(c alsa.Control, values []uint32) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		if c.Type == alsa.TypeEnumerated && int(v) < len(c.Items) {
			parts = append(parts, c.Items[v])
			continue
		}
		parts = append(parts, strconv.FormatUint(uint64(v), 10))
	}
	return strings.Join(parts, " ")
}
