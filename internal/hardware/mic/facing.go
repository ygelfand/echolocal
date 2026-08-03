package mic

import "math"

// Where beam 0 faces on the ring, in degrees clockwise from segment 0.
//
// Found by pointing it at things: the tap geometry said 252, which came out reflected, and a quarter turn
// the other way from there. Half a turn is the error an array this small is most prone to — at 36 mm the
// front and back of a beam differ by very little — and six beams can only report to the nearest 60
// degrees, so up to 30 of any apparent error is the quantising rather than this number.
const ringOffset = 320.0

// Facing is where the loudest sound is, as a fraction clockwise round the ring from segment 0, and
// whether that is known yet.
//
// Asking is what starts the work, so the first call comes back unknown and the next frame has an answer.
// Nothing keeps it going: a delay and sum per frame is not free and only the ring wants it.
func (s *Source) Facing() (float64, bool) {
	s.wantFacing.Store(true)

	beam := s.facing.Load()
	if beam < 0 {
		return 0, false
	}

	deg := float64(beam)*(360.0/Beams) - ringOffset
	return math.Mod(math.Mod(deg/360, 1)+1, 1), true
}

// findFacing steers the finder at whatever is loudest, if anything asked since the last frame. Its mix
// is discarded; only the direction it settled on is wanted.
//
// It switches the moment a direction wins rather than waiting for it to keep winning, which the mix does
// to avoid swinging mid-word. Nothing here is heard, so waiting only makes it remember where the last
// sound was — and a door closing or a hand clapping is over inside the wait. What steadies the ring is
// the effect easing towards the answer, which is the right place for it: the estimate says where the
// sound is now, and the animation decides how fast to believe it.
func (s *Source) findFacing(mics [][]int16) {
	if !s.wantFacing.Swap(false) {
		return
	}

	s.finder.Look(mics)
	s.facing.Store(int32(s.finder.Beam()))
}
