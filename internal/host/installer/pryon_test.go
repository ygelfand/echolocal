package installer

import (
	"reflect"
	"testing"
)

func TestUniqueLocales(t *testing.T) {
	got := unique([]string{"en-GB", "en", "en-GB", "", "en-US"})
	want := []string{"en-GB", "en", "en-US"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("\n/a/first\n/a/second\n"); got != "/a/first" {
		t.Fatalf("got %q", got)
	}
}

func TestPryonUserIDPattern(t *testing.T) {
	match := userIDPattern.FindStringSubmatch("Packages:\n  userId=32065\n")
	if len(match) != 2 || match[1] != "32065" {
		t.Fatalf("got %v", match)
	}
}

func TestPackagePath(t *testing.T) {
	tests := map[string]struct {
		output string
		want   string
	}{
		"visible": {
			output: "package:/system/priv-app/SpeechInteractionManager/SpeechInteractionManager.apk\n",
			want:   "/system/priv-app/SpeechInteractionManager/SpeechInteractionManager.apk",
		},
		"hidden": {
			output: "package:/system/priv-app/SpeechInteractionManager/SpeechInteractionManager.apk=amazon.speech.sim\n",
			want:   "/system/priv-app/SpeechInteractionManager/SpeechInteractionManager.apk",
		},
		"wrong package": {
			output: "package:/system/other.apk=example.other\n",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := packagePath(test.output, "amazon.speech.sim"); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestPryonRuntimeReadyRequiresLiveAudioEndToEnd(t *testing.T) {
	poc := "PRYON_CAPTURE_ACTIVE\nPRYON_SHARED_PCM_FIRST_FRAME\nPRYON_READY"
	helper := "shared Pryon capture first frame bytes=512"
	echo := "id=pryon_alexa engine=pryon"
	if !pryonRuntimeReady(poc, helper, echo) {
		t.Fatal("complete live microphone handshake was not ready")
	}

	tests := map[string]struct {
		poc, helper, echo string
	}{
		"native detector not ready": {poc: "PRYON_CAPTURE_ACTIVE\nPRYON_SHARED_PCM_FIRST_FRAME", helper: helper, echo: echo},
		"native recorder stalled":   {poc: "PRYON_SHARED_PCM_FIRST_FRAME\nPRYON_READY", helper: helper, echo: echo},
		"shared reader empty":       {poc: "PRYON_CAPTURE_ACTIVE\nPRYON_READY", helper: helper, echo: echo},
		"helper received no PCM":    {poc: poc, echo: echo},
		"Alexa not advertised":      {poc: poc, helper: helper},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if pryonRuntimeReady(test.poc, test.helper, test.echo) {
				t.Fatal("incomplete handshake reported ready")
			}
		})
	}
}
