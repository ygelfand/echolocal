package echod

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Dump the audio and hardware inventory",
		Long: "Everything needed to tell whether a unit matches the hardware we characterised,\n" +
			"and to diagnose the two failure modes that masquerade as broken hardware:\n" +
			"a PCM device held by mediaserver, and an LED ring with brightness set to 0.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			section(out, "ALSA cards")
			catFile(out, "/proc/asound/cards")

			section(out, "Capture (card 0 device 24)")
			catFile(out, "/proc/asound/card0/pcm24c/sub0/hw_params")
			reportHolder(out, "pcmC0D24c")

			section(out, "Playback (card 0 device 23)")
			catFile(out, "/proc/asound/card0/pcm23p/sub0/hw_params")
			reportHolder(out, "pcmC0D23p")

			section(out, "I2C devices")
			listI2C(out)

			section(out, "LED ring")
			for _, a := range []string{"frame", "led_current", "boot_animation"} {
				v := readTrim("/sys/bus/i2c/devices/0-003f/" + a)
				fmt.Fprintf(out, "%-15s %s\n", a+":", v)
			}

			section(out, "Platform")
			fmt.Fprintf(out, "%-15s %s\n", "selinux:", readTrim("/sys/fs/selinux/enforce"))
			fmt.Fprintf(out, "%-15s %d\n", "uid:", os.Getuid())
			return nil
		},
	}
}

func section(w io.Writer, name string) {
	fmt.Fprintf(w, "\n=== %s ===\n", name)
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(" + err.Error() + ")"
	}
	return strings.TrimSpace(string(b))
}

func catFile(w io.Writer, path string) {
	fmt.Fprintln(w, readTrim(path))
}

// reportHolder finds which process has a /dev/snd node open. Both PCM devices are normally
// held by mediaserver, which makes capture block and playback fail with EINVAL.
func reportHolder(w io.Writer, node string) {
	procs, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		return
	}
	for _, p := range procs {
		fds, err := filepath.Glob(p + "/fd/*")
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(fd)
			if err != nil || !strings.HasSuffix(target, node) {
				continue
			}
			pid := filepath.Base(p)
			cmdline := strings.ReplaceAll(readTrim(p+"/cmdline"), "\x00", " ")
			fmt.Fprintf(w, "held by:        pid %s %s\n", pid, cmdline)
			fmt.Fprintln(w, "                -> `stop media` to release it")
			return
		}
	}
	fmt.Fprintln(w, "held by:        nobody")
}

func listI2C(w io.Writer) {
	dirs, err := filepath.Glob("/sys/bus/i2c/devices/*")
	if err != nil {
		return
	}
	for _, d := range dirs {
		name := readTrim(d + "/name")
		if name == "" || strings.HasPrefix(name, "(") {
			continue
		}
		bound := ""
		if _, err := os.Readlink(d + "/driver"); err != nil {
			bound = "  (no driver bound)"
		}
		fmt.Fprintf(w, "%-12s %s%s\n", filepath.Base(d), name, bound)
	}
}
