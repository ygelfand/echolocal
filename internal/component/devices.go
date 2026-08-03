package component

// Sub-devices Home Assistant shows beneath this one, which entities join by Base.DeviceID. Zero is the
// device itself and is what most things want.
//
// The point is a shorter entity list per page, not grouping for its own sake: Home Assistant puts every
// entity of a device on one page, and there are enough of them here to be unreadable. Each of these is a
// real entry in the device registry, linked back to the device, so one is worth having only where it holds
// enough entities to be worth clicking into.
//
// Names are composed with the device's own at runtime. Home Assistant uses what it is given verbatim, so a
// bare "Ring" would be a device called "Ring" in a house with several satellites.
//
// The numbers are identity: Home Assistant keys a sub-device on the address and this, so changing one
// orphans the entry it used to name. Add above DeviceAssistant, which has to stay last because the slots
// count upwards from it.
const (
	DeviceRing uint32 = iota + 1

	// DeviceMicrophone is the array: how it is combined, how hard it is driven, whether it is cut.
	DeviceMicrophone

	// DevicePlayback is how sound comes out, which is not the media player itself — that is the thing
	// people reach for, and it stays where it is found.
	DevicePlayback

	// DeviceAssistant is the first of one per wake word slot. Home Assistant keeps its own Assistant and
	// Wake word selects on the device itself, so choosing a wake word and tuning it are on different pages.
	DeviceAssistant
)

// AssistantDevice is the sub-device holding one slot's settings.
func AssistantDevice(slot int) uint32 { return DeviceAssistant + uint32(slot) }
