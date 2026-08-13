package ble

import (
	"encoding/binary"
	"slices"
	"testing"
)

func TestCommandResultFindsBatchedCompletion(t *testing.T) {
	const opcode = 0x200a
	report := []byte{h4Event, evtLEMeta, 2, leAdvertisingReport, 0}
	completion := commandComplete(opcode, 0)
	batch := append(report, completion...)

	remainder, complete, err := commandResult(batch, opcode)
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("command did not complete")
	}
	if len(remainder) != 0 {
		t.Errorf("remainder = %x, want empty", remainder)
	}
}

func TestCommandResultHoldsSplitEvent(t *testing.T) {
	const opcode = 0x200a
	completion := commandComplete(opcode, 0)
	first := completion[:5]

	remainder, complete, err := commandResult(first, opcode)
	if err != nil {
		t.Fatal(err)
	}
	if complete {
		t.Fatal("partial command completed")
	}
	if !slices.Equal(remainder, first) {
		t.Errorf("remainder = %x, want %x", remainder, first)
	}

	remainder, complete, err = commandResult(append(remainder, completion[5:]...), opcode)
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("command did not complete after remainder")
	}
	if len(remainder) != 0 {
		t.Errorf("remainder = %x, want empty", remainder)
	}
}

func TestCommandResultReturnsTrailingPartialEvent(t *testing.T) {
	const opcode = 0x200a
	completion := commandComplete(opcode, 0)
	report := []byte{h4Event, evtLEMeta, 2, leAdvertisingReport, 0}
	batch := append(completion, report[:4]...)

	remainder, complete, err := commandResult(batch, opcode)
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("command did not complete")
	}
	if !slices.Equal(remainder, report[:4]) {
		t.Errorf("remainder = %x, want %x", remainder, report[:4])
	}

	event, remainder, ok, err := nextEvent(append(remainder, report[4:]...))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !slices.Equal(event, report) || len(remainder) != 0 {
		t.Errorf("event = %x, remainder = %x, ok = %t", event, remainder, ok)
	}
}

func TestCommandResultReturnsStatus(t *testing.T) {
	const opcode = 0x200a
	_, complete, err := commandResult(commandComplete(opcode, 0x0c), opcode)
	if !complete {
		t.Fatal("command did not complete")
	}
	if err == nil || err.Error() != "status 0x0c" {
		t.Errorf("error = %v, want status 0x0c", err)
	}
}

func TestCommandResultReturnsCommandStatusError(t *testing.T) {
	const opcode = 0x200a
	event := []byte{h4Event, evtCommandStatus, 4, 0x0c, 1, 0, 0}
	binary.LittleEndian.PutUint16(event[5:], opcode)

	_, complete, err := commandResult(event, opcode)
	if !complete {
		t.Fatal("command did not complete")
	}
	if err == nil || err.Error() != "status 0x0c" {
		t.Errorf("error = %v, want status 0x0c", err)
	}
}

func TestCommandResultRejectsMalformedEvent(t *testing.T) {
	const opcode = 0x200a
	batch := append([]byte{0xff}, commandComplete(opcode, 0)...)

	_, complete, err := commandResult(batch, opcode)
	if complete {
		t.Fatal("malformed stream completed")
	}
	if err == nil {
		t.Fatal("malformed stream returned no error")
	}
}

func commandComplete(opcode uint16, status byte) []byte {
	event := []byte{h4Event, evtCommandComplete, 4, 1, 0, 0, status}
	binary.LittleEndian.PutUint16(event[4:], opcode)
	return event
}
