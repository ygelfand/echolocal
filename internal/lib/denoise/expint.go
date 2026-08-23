package denoise

import "math"

// euler is the Euler-Mascheroni constant, which the series below is written in terms of.
const euler = 0.5772156649015328606

// e1 is the exponential integral E1(x) = ∫x..∞ exp(-t)/t dt, for x > 0.
//
// The gain rule needs it per band per frame, so it is the series near zero and a continued fraction
// away from it — the split the standard references use, since the series loses its accuracy as x
// grows and the fraction converges slowly as x falls.
func e1(x float64) float64 {
	switch {
	case x <= 0:
		return math.Inf(1)
	case x > 700:
		// exp(-x) has underflowed by here, and the integral with it.
		return 0
	case x <= 1:
		// Abramowitz and Stegun 5.1.11.
		sum, term := 0.0, 1.0
		for n := 1; n <= 30; n++ {
			term *= -x / float64(n)
			d := -term / float64(n)
			sum += d
			if math.Abs(d) < 1e-17*math.Abs(sum) {
				break
			}
		}
		return -euler - math.Log(x) + sum
	}

	// Modified Lentz for the continued fraction E1(x) = exp(-x) / (x + 1 - 1·1/(x + 3 - 2·2/(x + 5 - …))).
	const tiny = 1e-300
	b := x + 1
	c := 1 / tiny
	d := 1 / b
	h := d

	for i := 1; i <= 100; i++ {
		a := -float64(i * i)
		b += 2
		d = a*d + b
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + a/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		step := d * c
		h *= step
		if math.Abs(step-1) < 1e-15 {
			break
		}
	}
	return h * math.Exp(-x)
}
