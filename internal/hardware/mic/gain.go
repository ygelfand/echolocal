package mic

import (
	"fmt"
	"log/slog"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/lib/alsa"
)

// The array's analog gain. Turning it up buys level, not signal to noise: the floor is the room and
// the microphones, so both rise together. It costs headroom, so the default is the vendor's 20 dB.
const (
	// One control per ADC, each carrying both of its channels.
	pgaControls = "ADC_%s MICPGA Volume Ctrl"

	// Steps of half a decibel. The mixer declares a maximum of 80 and does not enforce it: the
	// register is seven bits, writes above 127 wrap, and 119 is the converter's own top of 59.5 dB.
	// Measured, the gain above 80 is real and costs no signal to noise.
	pgaStepDB = 0.5
	pgaMax    = 119
)

// adcs are the four converters the seven microphones arrive on.
var adcs = []string{"A", "B", "C", "D"}

// applyGain sets the analog gain on every ADC. A microphone that cannot be turned up is worth a log
// and nothing more: the array still works, quietly.
func applyGain(db int) {
	m, err := alsa.OpenMixer(Card)
	if err != nil {
		slog.Error("opening the mixer failed", "err", err)
		return
	}
	defer m.Close()

	steps := min(max(float64(db)/pgaStepDB, 0), pgaMax)
	for _, adc := range adcs {
		name := fmt.Sprintf(pgaControls, adc)
		if err := m.SetInt(name, uint32(steps)); err != nil {
			slog.Error("setting the microphone gain failed", "control", name, "err", err)
			return
		}
	}
	slog.Info("microphone gain", "db", db)
}

// SetGain changes the analog gain and remembers it.
func SetGain(db int) error {
	if err := config.Set().Microphone().Gain(db); err != nil {
		return err
	}
	applyGain(db)
	return nil
}
