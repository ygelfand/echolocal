package component

// Sub-devices Home Assistant shows beneath this one, which entities join by Base.DeviceID. Zero is the
// device itself and is what most things want.
//
// The point is a shorter entity list per page, not grouping for its own sake: Home Assistant puts every
// entity of a device on one page, and there are enough of them here to be unreadable. Each of these is a
// real entry in the device registry, linked back to the device, so one is worth having only where it
// holds enough entities to be worth clicking into.
//
// Names are composed with the device's own at runtime. Home Assistant uses what it is given verbatim, so
// a bare "Ring" would be a device called "Ring" in a house with several satellites.
const (
	DeviceRing uint32 = 1
)
