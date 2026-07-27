// Package services hides the Amazon packages that hold hardware echod needs.
package services

// Hidden is the set provisioning hides, recovered from a device where it was established by
// hand and verified to survive reboots.
//
// `pm hide` persists, so this is applied once at install rather than re-applied each boot. It is
// paired with a force-stop because hiding a package blocks future launches but leaves a running
// process alive, and a live audio client keeps mediaserver holding the PCM devices.
//
// Note two entries are not com.amazon.* — a prefix filter would miss them.
var Hidden = []Package{
	{"amazon.speech.davs.davcservice", "Alexa voice service; holds capture"},
	{"amazon.speech.sim", "owns the mute button and mic mute state"},
	{"com.amazon.device.echoaudioservice", "audio service; holds the PCM devices"},
	{"com.amazon.mediaplayeragent", "playback agent"},
	{"com.amazon.alexa.externalmediaplayer.fireos", "playback agent"},
	{"com.amazon.alexa.beaconbroadcaster", "Alexa beacon broadcast"},
	{"com.amazon.comms.headlesstachyon", "calling and messaging"},
	{"com.amazon.spotify.mediabrowserservice", "media browser"},
	{"com.amazon.wha.mediabrowserservice", "media browser"},
	{"com.amazon.device.smarthome.dshs.services", "smart home service"},
	{"com.amazon.device.smarthome.dshs.headlessux", "smart home UX"},
	{"com.amazon.device.smarthome.dshs.endpointdetectorCA", "smart home endpoint detection"},
	{"com.amazon.device.smarthome.adapters.ble", "smart home BLE adapter"},
	{"com.amazon.device.smarthome.adapters.echo", "smart home Echo adapter"},
	{"com.amazon.device.smarthome.ota", "smart home OTA"},
}

// Package is one package and why it goes.
type Package struct {
	Name   string
	Reason string
}

// Names lists the package names.
func Names() []string {
	out := make([]string, 0, len(Hidden))
	for _, p := range Hidden {
		out = append(out, p.Name)
	}
	return out
}
