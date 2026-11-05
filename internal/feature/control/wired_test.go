//go:build !board_biscuit

package control

import (
	"reflect"
	"testing"

	"github.com/ygelfand/libcountertop/pkg/settings/settingtest"

	"github.com/ygelfand/echolocal/internal/config"
)

var coverage = settingtest.Shared.With(settingtest.Coverage{
	Rows: map[string]string{
		"Microphone.Gain":           "microphone.gain",
		"Microphone.Leveling":       "microphone.leveling",
		"Microphone.Mixing":         "microphone.mixing",
		"Microphone.Cancel":         "microphone.cancel",
		"Microphone.Sensitivity":    "microphone.sensitivity",
		"Microphone.LEDBright":      "microphone.mute_led",
		"Microphone.MuteSound":      "microphone.mute_sound",
		"Microphone.VisualizerLift": "visual.lift",
		"Ring.Reaction":             "ring.reaction",
		"Ring.Trouble":              "ring.trouble",
		"Ring.Muted":                "ring.muted",
		"Ring.Incoming":             "ring.incoming",
		"Ring.Talking":              "ring.talking",
		"Speaker.Resampling":        "speaker.resampling",
		"Speaker.ASP":               "speaker.eq",
		"Speaker.SpeakerOutput":     "speaker.speaker_output",
		"Speaker.HeadphoneOutput":   "speaker.headphone_output",
	},
	ByHand: map[string]string{
		"Device.Name":  "told to the process at start-up, never written to the file",
		"Device.Addr":  "the same",
		"Device.Board": "the same, read from the hardware",

		"Speaker.Volume":   "the volume buttons and Home Assistant's media player",
		"Volume.Media":     "the volume command, by stream",
		"Volume.Voice":     "the volume command",
		"Volume.Alerts":    "the volume command",
		"Volume.Feedback":  "the volume command",
		"Microphone.Muted": "the mute button and Home Assistant's mute switch",

		"Ring.Light.On":         "Home Assistant's light entity for the ring",
		"Ring.Light.Brightness": "the same",
		"Ring.Light.Red":        "the same",
		"Ring.Light.Green":      "the same",
		"Ring.Light.Blue":       "the same",
		"Ring.Light.Effect":     "the same",

		"Lounge.Device":   "the Echo Dot's YouTube lounge; the Shows run none",
		"Lounge.Music":    "the same",
		"Lounge.Video":    "the same",
		"Lounge.OnDemand": "the same",
	},
})

func TestEverySettingIsReachedOrAccountedFor(t *testing.T) {
	coverage.Accounts(t, reflect.TypeFor[config.Config]())
}

func TestEverySettingSaysWhatItChanges(t *testing.T) {
	var names []string
	for _, s := range settings() {
		names = append(names, s.Name)
	}
	coverage.Names(t, names)
}
