package led

// The catalogue: every pairing of a motion with the colours it runs in, in the order Home Assistant is
// offered them. Each motion is a file of its own next to this one; this is the table of contents, so
// that what exists and what it is called are in one place rather than spread across the files that draw
// them. Adding an effect is a file and a line here.

// ambientEffects are the ones that can be left running. Nothing here moves quickly, jumps, or draws
// the eye: a ring in the corner of a room is being looked at all evening, so an ambient effect is
// judged by whether it can be ignored, not by how much is happening.
var ambientEffects = []Effect{
	// In the ring's own colour.
	{Name: EffectPulse, New: breathe},
	{Name: EffectHeartbeat, New: heartbeat},
	{Name: EffectRipple, New: ripple},
	{Name: EffectStandingWave, New: standing},
	{Name: EffectTwinkle, New: twinkle},

	// In colours of their own, where the colours are the point: a flame is not a shape, and an
	// aurora in one colour is a ring that cannot make up its mind.
	{Name: EffectCrimsonHeartbeat, Palette: crimson, New: heartbeat},
	{Name: EffectAuroraPulse, Palette: aurora, New: breathe},
	{Name: EffectCandle, Palette: flame, New: flicker},
	{Name: EffectFireplace, Palette: fire, New: flicker},
	{Name: EffectEmbers, Palette: fire, New: embers},
	{Name: EffectAurora, Palette: aurora, New: drift},
	{Name: EffectSunsetDrift, Palette: sunset, New: drift},
	{Name: EffectOceanRipple, Palette: ocean, New: ripple},
	{Name: EffectRainbowTwinkle, Palette: wheel, New: twinkle},
	{Name: EffectForestTwinkle, Palette: forest, New: twinkle},
}

// motionEffects travel. Something going round the ring is how the device says it is busy, so these
// are the ones a conversation runs, and they are the ones worth looking at for direction: the driver
// can play any of them backwards, which is how listening and waiting are told apart.
var motionEffects = []Effect{
	// In the ring's own colour.
	{Name: EffectComet, New: comet},
	{Name: EffectChase, New: chase},
	{Name: EffectScanner, New: scanner},
	{Name: EffectPinwheel, New: pinwheel},
	{Name: EffectSpiral, New: spiral},
	{Name: EffectWipe, New: wipe},
	{Name: EffectHelix, New: helix},
	{Name: EffectOrbits, New: orbits},
	{Name: EffectBounce, New: bounce},
	{Name: EffectSpring, New: spring},

	// In colours of their own.
	{Name: EffectRainbow, Palette: wheel, New: spin},
	{Name: EffectFireComet, Palette: fire, New: comet},
	{Name: EffectIceComet, Palette: ice, New: comet},
	{Name: EffectRainbowChase, Palette: wheel, New: chase},
	{Name: EffectIceScanner, Palette: ice, New: scanner},
	{Name: EffectRainbowPinwheel, Palette: wheel, New: pinwheel},
	{Name: EffectSunsetSpiral, Palette: sunset, New: spiral},
	{Name: EffectRainbowOrbits, Palette: wheel, New: orbits},
	{Name: EffectDNA, Palette: duo, New: helix},
	{Name: EffectPacMan, Palette: pellets, New: pacman},
}

// alertEffects are announcements: shapes meant to be noticed rather than lived with. They are in the
// light's list like everything else, but what they are for is the failure indication and anything else
// the device needs to say happened.
var alertEffects = []Effect{
	{Name: EffectAlert, Palette: alarm, New: alert},
	{Name: EffectBeacon, Palette: alarm, New: beacon},
}

// roomEffects react to the room rather than to the clock. They are what a device that is always
// listening can honestly show: not a guess at what is happening, just how loud it is in here.
//
// None of them can be a light effect, because none of them mean anything without something feeding
// them. They are chosen from their own control and are simply on while chosen.
var roomEffects = []Effect{
	{Name: EffectRoomGlow, Senses: roomGlow},
	{Name: EffectRoomMeter, Senses: roomMeter},
	{Name: EffectRoomOcean, Palette: ocean, Senses: roomMeter},
	{Name: EffectRoomVU, Palette: vu, Senses: roomMeter},
	{Name: EffectRoomFire, Palette: fire, Senses: roomFire},
	{Name: EffectRoomSpin, Palette: wheel, Senses: roomSpin},

	// Any motion in the catalogue can be a room effect, since all the room has to do is decide how
	// much of it shows. These are the ones worth offering rather than all of them.
	{Name: EffectRoomAurora, Palette: aurora, Senses: byRoom(drift)},
	{Name: EffectRoomTwinkle, Palette: wheel, Senses: byRoom(twinkle)},
	{Name: EffectRoomEmbers, Palette: fire, Senses: byRoom(embers)},
}
