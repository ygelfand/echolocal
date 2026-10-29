package echod

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ygelfand/libcountertop/pkg/system/i2c"
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

	var bus int
	c.PersistentFlags().IntVarP(&bus, "bus", "b", 0, "I2C bus number")

	device := func(addr uint8) (*i2c.Device, func(), error) {
		b, err := i2c.Open(bus)
		if err != nil {
			return nil, nil, err
		}
		d, err := i2c.At(b, uint16(addr))
		if err != nil {
			_ = b.Close()
			return nil, nil, err
		}
		return d, func() { _ = b.Close() }, nil
	}

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

			if last < first {
				return fmt.Errorf("i2c: %d..%d is not a range", first, last)
			}
			d, done, err := device(addr)
			if err != nil {
				return err
			}
			defer done()

			regs := make([]byte, 0, int(last-first)+1)
			for r := int(first); r <= int(last); r++ {
				v, err := d.Byte(byte(r))
				if err != nil {
					v = 0xff
				}
				regs = append(regs, v)
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

			d, done, err := device(addr)
			if err != nil {
				return err
			}
			defer done()

			before, readable := d.Byte(byte(reg))
			if err := d.Write(byte(reg), byte(val)); err != nil {
				return err
			}

			// The LED driver at 0x3f answers writes and NAKs every read with ENXIO.
			out := cmd.OutOrStdout()
			after, err := d.Byte(byte(reg))
			switch {
			case readable != nil || err != nil:
				fmt.Fprintf(
					out,
					"0x%02x reg 0x%02x: wrote %02x (write only, cannot be read back)\n",
					addr,
					reg,
					val,
				)
			case after != uint8(val):
				fmt.Fprintf(
					out,
					"0x%02x reg 0x%02x: %02x -> %02x  (wanted %02x, write did not stick)\n",
					addr,
					reg,
					before,
					after,
					val,
				)
			default:
				fmt.Fprintf(out, "0x%02x reg 0x%02x: %02x -> %02x\n", addr, reg, before, after)
			}
			return nil
		},
	}

	c.AddCommand(dump, set)
	return c
}
