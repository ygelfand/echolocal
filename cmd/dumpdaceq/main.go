// dumpdaceq reads the DAC biquad coefficient blob out of a TLV320AIC3x codec and prints it as a
// Go-style byte slice, suitable for pasting straight into the speakerEQ literal in paths.go.
//
// Without arguments it reads card 0 control "biquad coefficients". The card and name can be
// overridden for testing.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ygelfand/echolocal/internal/lib/alsa"
)

func main() {
	card := flag.Int("card", 0, "ALSA card number")
	name := flag.String("name", "biquad coefficients", "byte control to read")
	flag.Parse()

	m, err := alsa.OpenMixer(*card)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open mixer:", err)
		os.Exit(1)
	}
	defer m.Close()

	data, err := m.GetBytes(*name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}

	fmt.Printf("// %s on card %d: %d bytes\n", *name, *card, len(data))
	fmt.Printf("var speakerEQ_%s = []byte{\n", codecHint())
	for i := 0; i < len(data); i += 12 {
		end := i + 12
		if end > len(data) {
			end = len(data)
		}
		var b strings.Builder
		for j, x := range data[i:end] {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%d", x)
		}
		fmt.Printf("\t%s,\n", b.String())
	}
	fmt.Println("}")
}

// codecHint picks a Go identifier suffix from the device name. Best-effort: a real tool would
// take this on the command line, but on the device we are inside adb anyway.
func codecHint() string {
	if data, err := os.ReadFile("/proc/device-tree/model"); err == nil {
		m := strings.ToLower(string(data))
		switch {
		case strings.Contains(m, "radar"):
			return "radar"
		case strings.Contains(m, "biscuit"):
			return "biscuit"
		}
	}
	return "device"
}
