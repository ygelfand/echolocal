package metrics

import (
	"path/filepath"
	"strconv"
)

// LuxPath is the file the light sensor's reading comes from, empty when the board has none. Two parts
// turn up across units: a tsl2584tsv on an IIO driver, or a tsl2540 on a vendor one with no IIO device
// at all. Looked for once, at start-up.
//
// Reading the calibrated value either driver offers beside these crashes the device.
func (r Reader) LuxPath() string {
	for i := range 8 {
		at := r.path("sys/bus/iio/devices/iio:device"+strconv.Itoa(i)) + "/illuminance0_input"

		if _, err := number(at); err == nil {
			return at
		}
	}

	vendor, _ := filepath.Glob(r.path("sys/bus/i2c/devices/*/als_lux"))
	for _, at := range vendor {
		if _, err := number(at); err == nil {
			return at
		}
	}
	return ""
}

// Lux is how bright the room is, read from the file LuxPath found.
//
// The driver holds a converted value and hands back the last one, so a read costs about ten
// milliseconds rather than the integration time. There is no buffer, trigger or event to wait on, so
// asking is the only way to have the number.
func (r Reader) Lux(path string) Reading {
	if path == "" {
		return Reading{}
	}

	lux, err := number(path)
	if err != nil {
		return Reading{}
	}
	return known(lux)
}
