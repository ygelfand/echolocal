package boot

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/ygelfand/echolocal/internal/gpio"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/service"
	"github.com/ygelfand/echolocal/internal/speaker"
)

// Taking the hardware. Whatever is free, Android takes, so each of these is held for the life of the
// process rather than opened per use.

// prepareRing takes the ring off the driver that has been animating it since power-on.
func prepareRing() *led.Ring {
	ring := led.New()

	// The driver animates the ring itself from power-on and keeps repainting over whatever we write,
	// including our blank frames. Amazon's ledcontroller turned this off before driving frames; taking
	// its place means taking that over too.
	if err := ring.SetBootAnimation(false); err != nil {
		slog.Error("disabling driver boot animation failed", "err", err)
	}

	// ledctrl leaves the global drive current at 0, where frame writes are accepted and read back
	// correctly but nothing lights up. 3 is the driver's own default.
	if cur, err := ring.Current(); err != nil {
		slog.Error("reading led_current failed", "err", err)
	} else if cur == 0 {
		slog.Info("led_current is 0, raising to 3")
		if err := ring.SetCurrent(3); err != nil {
			slog.Error("setting led_current failed", "err", err)
		}
	}
	return ring
}

func takeMute() (*gpio.Mute, *gpio.MuteLED) {
	mute, err := gpio.NewMute()
	if err != nil {
		slog.Error("mute unavailable", "err", err)
	}

	muteLED, err := gpio.NewMuteLED()
	if err != nil {
		slog.Error("mute LED unavailable", "err", err)
		return mute, nil
	}
	if err := muteLED.SetBright(true); err != nil {
		slog.Error("setting mute LED bright failed", "err", err)
	}
	return mute, muteLED
}

// takeSpeaker holds the playback stream open for the life of the process, feeding silence when idle:
// the amplifier hisses when nothing drives the DAC, and toggling it pops.
//
// Neither this nor the microphones restart on failure. They are acquired here rather than by the
// service, so a restart would re-run the loop against a handle that is already broken; making the
// handle outlive the device is what would turn them into restartable services.
func takeSpeaker(group *service.Group) *speaker.Player {
	spk, err := speaker.Acquire()
	if err != nil {
		slog.Error("speaker unavailable", "err", err)
		return nil
	}
	group.Add(runner{name: "speaker", run: spk.Run, close: spk.Close})
	return spk
}

func takeMicrophones(group *service.Group) *mic.Source {
	source, err := mic.Acquire()
	if err != nil {
		slog.Error("microphones unavailable", "err", err)
		return nil
	}
	group.Add(runner{name: "capture", run: source.Run, close: source.Close})
	return source
}

// listenAddr is the port the installer opened in the firewall.
func listenAddr() string { return fmt.Sprintf(":%d", layout.Port) }

// name prefers what echoctl recorded at install, since Home Assistant keys the device on it.
func name(override string) string {
	if override != "" {
		return override
	}
	if b, err := os.ReadFile(layout.NamePath); err == nil {
		if recorded := strings.TrimSpace(string(b)); recorded != "" {
			return recorded
		}
	}

	b, err := os.ReadFile(layout.MACPath)
	if err != nil {
		return layout.DefaultName
	}
	return layout.NameFromMAC(string(b))
}
