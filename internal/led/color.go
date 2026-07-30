package led

import "math"

// HomeAssistant is Home Assistant's brand blue.
var HomeAssistant = Color{R: 0x18, G: 0xBC, B: 0xF2}

// Arc lights a clockwise fraction of the ring, from 0 to 1, dimming the leading segment by
// whatever is left over. Twelve segments cannot show thirty volume steps, so the partial
// segment is what makes each step visible — the same trick Amazon's firmware used.
//
// It fills from segment 11 so the arc grows across the front of the device.
func Arc(fraction float64, c Color) []Color { return arcOf(fraction, Palette{c}) }

// Volume is the level as an arc in the meter's colours: green while quiet, amber as it gets loud,
// red at the top. Since arcOf takes the colour from where a segment sits rather than from how far
// along the fill it is, the same step is always the same colour, and how loud the device is about to
// be can be read without counting segments.
func Volume(fraction float64) []Color { return arcOf(fraction, vu) }

// arcOf is the same arc in a palette's colours, laid along the fill rather than round the ring: the
// far end of the arc is the far end of the palette, so how full it is says something on its own.
func arcOf(fraction float64, p Palette) []Color {
	fraction = math.Max(0, math.Min(1, fraction))
	lit := fraction * Segments

	out := make([]Color, Segments)
	for i := range Segments {
		remaining := lit - float64(i)
		if remaining <= 0 {
			break
		}
		part := math.Min(1, remaining)
		out[(11+i)%Segments] = scale(p.Along(float64(i)/(Segments-1)), part)
	}
	return out
}

// scale dims a colour. The channels are linear brightness, so this is the whole of it.
func scale(c Color, f float64) Color {
	clamp := func(v byte) byte { return byte(math.Round(math.Max(0, math.Min(255, float64(v)*f)))) }
	return Color{R: clamp(c.R), G: clamp(c.G), B: clamp(c.B)}
}

// add lays one colour on top of another, holding at full scale. It is what two lights in the same
// place do, so effects with more than one thing moving add rather than assign: two dots crossing
// brighten each other instead of one erasing the other for a frame.
func add(a, b Color) Color {
	sum := func(x, y byte) byte {
		if int(x)+int(y) > 255 {
			return 255
		}
		return x + y
	}
	return Color{R: sum(a.R, b.R), G: sum(a.G, b.G), B: sum(a.B, b.B)}
}

// blend mixes two colours, f of the way from a to b. The channels are linear brightness, so there is
// nothing to it but interpolation.
func blend(a, b Color, f float64) Color {
	f = math.Max(0, math.Min(1, f))
	mix := func(x, y byte) byte {
		return byte(math.Round(float64(x) + (float64(y)-float64(x))*f))
	}
	return Color{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B)}
}

// hue converts a 0-1 position on the color wheel to full-saturation RGB.
func hue(h float64) Color {
	x := math.Mod(h*6, 6)
	ramp := func(v float64) byte { return byte(math.Round(math.Max(0, math.Min(1, v)) * 255)) }
	switch int(x) {
	case 0:
		return Color{R: 255, G: ramp(x), B: 0}
	case 1:
		return Color{R: ramp(2 - x), G: 255, B: 0}
	case 2:
		return Color{R: 0, G: 255, B: ramp(x - 2)}
	case 3:
		return Color{R: 0, G: ramp(4 - x), B: 255}
	case 4:
		return Color{R: ramp(x - 4), G: 0, B: 255}
	default:
		return Color{R: 255, G: 0, B: ramp(6 - x)}
	}
}
