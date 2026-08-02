package led

import "math"

// Palette is the colours an effect draws from: either the one Home Assistant set on the ring, or
// the effect's own. Asking a palette for a colour rather than dimming one it was handed is what
// lets a single frame function serve both.
//
// Sampled either round the ring, where the ends have to meet, or along a gradient.
type Palette []Color

// At samples the palette as a circle, blending between neighbours and wrapping past the end, so a
// gradient laid around the ring meets itself without a seam.
func (p Palette) At(x float64) Color {
	switch len(p) {
	case 0:
		return Color{}
	case 1:
		return p[0]
	}

	x = math.Mod(math.Mod(x, 1)+1, 1) * float64(len(p))
	i := int(x)
	return blend(p[i], p[(i+1)%len(p)], x-float64(i))
}

// Along samples it as a gradient with two ends, for a palette that runs from one colour to another
// rather than round. Wrapping such a palette puts its ends next to each other, which on a flame is
// the coldest red touching the hottest white.
func (p Palette) Along(x float64) Color {
	if len(p) < 2 {
		return p.At(x)
	}

	x = math.Max(0, math.Min(1, x)) * float64(len(p)-1)
	i := int(x)
	if i >= len(p)-1 {
		return p[len(p)-1]
	}
	return blend(p[i], p[i+1], x-float64(i))
}

// Shade samples the palette along its length at x and dims what it finds by dim, for the parts of an
// effect that fade: the trough between two crests, the tail behind a head, the edge of a beam.
//
// Both arguments say the same thing twice on purpose, because there are two ways to fade and only
// one of them is available at a time. A palette of one colour has nowhere to go, so less light is the
// only fade it has, and it gets dim in full. A palette that already cools along its length would then
// be dimming a colour that is dark to begin with, which leaves the far end black and throws away the
// colours that were the reason for choosing it — so there, dim only takes the edge off.
func (p Palette) Shade(x, dim float64) Color {
	if len(p) > 1 {
		dim = 0.5 + 0.5*dim
	}
	return scale(p.Along(x), dim)
}

// Nth picks one colour whole, for effects with countable parts — the lamps of a marquee, the arms of
// a pinwheel — where blending between them would only muddy the count.
func (p Palette) Nth(i int) Color {
	if len(p) == 0 {
		return Color{}
	}
	return p[((i%len(p))+len(p))%len(p)]
}

// The palettes effects come with. They are gathered here rather than sitting next to the animations
// because choosing colours that work together on this ring is a different job from writing the
// motion, and because a palette is worth reusing across several.

// wheel is every hue, for effects that mean to show all of them. Six stops rather than a smooth
// sweep because hue is piecewise linear between the primaries and secondaries, so six is exactly
// enough for the blend to land on the same colours the wheel would.
//
// It sits at 0.6 of full scale, which is where the ring's rainbow has always been: these LEDs at
// full on every channel are glaring rather than colourful.
var wheel = func() Palette {
	p := make(Palette, 6)
	for i := range p {
		p[i] = scale(hue(float64(i)/float64(len(p))), 0.6)
	}
	return p
}()

// flame is one colour, roughly a candle at 1900 K.
var flame = Palette{{R: 0xFF, G: 0x93, B: 0x29}}

// fire runs from the near-white at the base of a flame out through orange to the dull red at its
// edge, which is what makes a fire read as depth rather than as an orange ring going on and off.
// Sampled along, not round, and hottest first: an effect fading away fades toward the far end.
var fire = Palette{
	{R: 0xFF, G: 0xE0, B: 0xA0},
	{R: 0xFF, G: 0x90, B: 0x20},
	{R: 0xD0, G: 0x30, B: 0x00},
	{R: 0x40, G: 0x04, B: 0x00},
}

// crimson is blood: nearly black at rest, up to a scarlet with enough white in it to read as a hard
// beat. Sampled along, darkest first, so an effect whose shape is how hard something is happening
// climbs it.
var crimson = Palette{
	{R: 0x28, G: 0x00, B: 0x04},
	{R: 0x80, G: 0x00, B: 0x08},
	{R: 0xD0, G: 0x00, B: 0x14},
	{R: 0xFF, G: 0x40, B: 0x48},
}

// aurora is the greens and violets of one, in the order they sit in the sky. It closes back on green
// so it can be sampled round the ring without a seam.
var aurora = Palette{
	{R: 0x00, G: 0xC8, B: 0x50},
	{R: 0x00, G: 0xB0, B: 0xB0},
	{R: 0x30, G: 0x40, B: 0xC0},
	{R: 0x70, G: 0x20, B: 0xA0},
	{R: 0x10, G: 0x90, B: 0x80},
}

// ocean is deep water up to the light on top of it, for a wave that should look like it has
// something underneath. Sampled along, deepest first, so a crest reaches the far end.
var ocean = Palette{
	{R: 0x00, G: 0x0C, B: 0x40},
	{R: 0x00, G: 0x50, B: 0xA0},
	{R: 0x00, G: 0xB0, B: 0xC0},
	{R: 0x90, G: 0xF0, B: 0xE0},
}

// ice is the cold end of white: a blue shadow up to the glare off a surface. Hottest last, unlike
// fire, because ice reads as bright rather than as warm.
var ice = Palette{
	{R: 0x08, G: 0x18, B: 0x50},
	{R: 0x10, G: 0x60, B: 0xC0},
	{R: 0x60, G: 0xC0, B: 0xF0},
	{R: 0xE0, G: 0xF8, B: 0xFF},
}

// sunset runs the sky at dusk from the violet overhead down to the gold at the horizon. Sampled
// along, darkest first.
var sunset = Palette{
	{R: 0x30, G: 0x08, B: 0x60},
	{R: 0xA0, G: 0x10, B: 0x60},
	{R: 0xE0, G: 0x50, B: 0x20},
	{R: 0xFF, G: 0xB0, B: 0x30},
}

// forest is the greens of one, deep shade to new growth, with none of the blue that makes an LED
// green look like a screen.
var forest = Palette{
	{R: 0x04, G: 0x28, B: 0x10},
	{R: 0x10, G: 0x70, B: 0x20},
	{R: 0x40, G: 0xB0, B: 0x20},
	{R: 0xA0, G: 0xE0, B: 0x40},
}

// alarm is the red of a fault, from the dark red it rests at up to the near-white at the top of a
// pulse. Sampled along and darkest first, so an effect whose shape is how hard something is happening
// climbs it. Red because nothing else on this device is: the splash is blue, the volume arc white,
// and a colour used for one thing only needs no explaining.
var alarm = Palette{
	{R: 0x30, G: 0x00, B: 0x00},
	{R: 0xC0, G: 0x00, B: 0x00},
	{R: 0xFF, G: 0x30, B: 0x18},
	{R: 0xFF, G: 0xB0, B: 0xA0},
}

// vu is a meter scale: green where a room sits, amber where it is busy, red at the top. Sampled
// along, since the whole point is that the far end means something different from the near end.
var vu = Palette{
	{R: 0x00, G: 0xB0, B: 0x18},
	{R: 0x50, G: 0xC0, B: 0x00},
	{R: 0xE0, G: 0x90, B: 0x00},
	{R: 0xFF, G: 0x10, B: 0x00},
}

// duo is two colours far enough apart to stay separate at a glance, for effects whose whole shape is
// two of something. Sampled with Nth, never blended: the point is telling them apart.
var duo = Palette{
	{R: 0x00, G: 0xE0, B: 0xC0},
	{R: 0xE0, G: 0x00, B: 0xA0},
}

// pellets is Pac-Man: the yellow of him, and the dim white of what he has not eaten yet. Sampled
// with Nth — these are two things, not a gradient.
var pellets = Palette{
	{R: 0xFF, G: 0xD0, B: 0x00},
	{R: 0x30, G: 0x30, B: 0x38},
}
