package echod

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/lib/i2c"
)

func newI2CCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "i2c",
		Short: "Read I2C registers directly",
		Long: "Sees hardware state that no driver exposes. The four TLV320AIC3101 mic codecs at\n" +
			"0x18-0x1b on bus 0 have no regmap debugfs, so registers Amazon's firmware writes\n" +
			"directly are invisible any other way.\n\n" +
			"Reads are safe. Writing races whatever driver owns the device.",
	}

	var (
		bus   int
		force bool
	)
	c.PersistentFlags().IntVarP(&bus, "bus", "b", 0, "I2C bus number")
	c.PersistentFlags().BoolVar(&force, "force", true, "claim addresses owned by a kernel driver")

	parseAddr := func(s string) (uint8, error) {
		v, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 8)
		return uint8(v), err
	}

	dump := &cobra.Command{
		Use:   "dump <addr-hex> [first] [last]",
		Short: "Dump a register range as hex",
		Args:  cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := parseAddr(args[0])
			if err != nil {
				return err
			}
			first, last := uint8(0), uint8(127)
			if len(args) > 1 {
				v, err := strconv.Atoi(args[1])
				if err != nil {
					return err
				}
				first = uint8(v)
			}
			if len(args) > 2 {
				v, err := strconv.Atoi(args[2])
				if err != nil {
					return err
				}
				last = uint8(v)
			}

			b, err := i2c.Open(bus, addr, force)
			if err != nil {
				return err
			}
			defer func() { _ = b.Close() }()

			regs, err := b.Dump(first, last)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			for i, v := range regs {
				r := int(first) + i
				if r%16 == 0 {
					fmt.Fprintf(out, "\n%02x:", r)
				}
				fmt.Fprintf(out, " %02x", v)
			}
			fmt.Fprintln(out)
			return nil
		},
	}

	set := &cobra.Command{
		Use:   "set <addr-hex> <reg-hex> <val-hex>",
		Short: "Write one register",
		Long: "Writes race whatever kernel driver owns the device. The stock firmware writes the\n" +
			"mic codecs this way itself, so it is an established path here, but it is still a\n" +
			"blind write with no driver coordination.",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := parseAddr(args[0])
			if err != nil {
				return err
			}
			reg, err := strconv.ParseUint(strings.TrimPrefix(args[1], "0x"), 16, 8)
			if err != nil {
				return err
			}
			val, err := strconv.ParseUint(strings.TrimPrefix(args[2], "0x"), 16, 8)
			if err != nil {
				return err
			}

			b, err := i2c.Open(bus, addr, force)
			if err != nil {
				return err
			}
			defer func() { _ = b.Close() }()

			before, readable := b.ReadReg(uint8(reg))
			if err := b.WriteReg(uint8(reg), uint8(val)); err != nil {
				return err
			}

			// Some of these chips answer writes and refuse reads — the LED driver at 0x3f is one, and
			// NAKs every read with ENXIO. So the write is the result, and reading it back is a bonus:
			// failing here would report a write that landed as an error.
			out := cmd.OutOrStdout()
			after, err := b.ReadReg(uint8(reg))
			switch {
			case readable != nil || err != nil:
				fmt.Fprintf(out, "0x%02x reg 0x%02x: wrote %02x (write only, cannot be read back)\n", addr, reg, val)
			case after != uint8(val):
				fmt.Fprintf(out, "0x%02x reg 0x%02x: %02x -> %02x  (wanted %02x, write did not stick)\n",
					addr, reg, before, after, val)
			default:
				fmt.Fprintf(out, "0x%02x reg 0x%02x: %02x -> %02x\n", addr, reg, before, after)
			}
			return nil
		},
	}

	c.AddCommand(dump, set)
	return c
}
