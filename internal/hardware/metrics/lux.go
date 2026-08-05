package metrics

import "strconv"

// LuxPath is the file the light sensor's reading comes from, empty when the board has none. The board
// declares two and binds whichever one is fitted, so which index it lands on has to be looked for —
// once, at start-up, rather than on every reading.
func (r Reader) LuxPath() string {
	for i := range 8 {
		at := r.path("sys/bus/iio/devices/iio:device"+strconv.Itoa(i)) + "/illuminance0_input"

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
