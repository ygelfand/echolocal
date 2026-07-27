package prop

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestSetRejectsOversizedFields(t *testing.T) {
	if err := Set(strings.Repeat("n", nameMax), "1"); err == nil {
		t.Error("want error for oversized name")
	}
	if err := Set("ctl.stop", strings.Repeat("v", valueMax)); err == nil {
		t.Error("want error for oversized value")
	}
}

// The message layout is the whole protocol, and a device is the only other way to check it.
func TestMessageLayout(t *testing.T) {
	msg := make([]byte, msgLen)
	binary.LittleEndian.PutUint32(msg[:4], cmdSetProp)
	copy(msg[4:4+nameMax-1], "ctl.stop")
	copy(msg[4+nameMax:msgLen-1], "ledcontroller")

	if len(msg) != 128 {
		t.Fatalf("prop_msg is %d bytes, want 128", len(msg))
	}
	if got := binary.LittleEndian.Uint32(msg[:4]); got != 1 {
		t.Errorf("cmd = %d, want 1 (PROP_MSG_SETPROP)", got)
	}
	if got := string(msg[4:12]); got != "ctl.stop" {
		t.Errorf("name = %q, want %q", got, "ctl.stop")
	}
	if msg[4+nameMax-1] != 0 {
		t.Error("name field must stay NUL-terminated")
	}
	if got := string(msg[36:49]); got != "ledcontroller" {
		t.Errorf("value = %q, want %q", got, "ledcontroller")
	}
	if msg[msgLen-1] != 0 {
		t.Error("value field must stay NUL-terminated")
	}
}
