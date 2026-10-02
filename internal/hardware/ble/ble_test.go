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

	remainder, _, complete, err := commandResult(batch, opcode)
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

	remainder, _, complete, err := commandResult(first, opcode)
	if err != nil {
		t.Fatal(err)
	}
	if complete {
		t.Fatal("partial command completed")
	}
	if !slices.Equal(remainder, first) {
		t.Errorf("remainder = %x, want %x", remainder, first)
	}

	remainder, _, complete, err = commandResult(append(remainder, completion[5:]...), opcode)
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

	remainder, _, complete, err := commandResult(batch, opcode)
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
	_, _, complete, err := commandResult(commandComplete(opcode, 0x0c), opcode)
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

	_, _, complete, err := commandResult(event, opcode)
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

	_, _, complete, err := commandResult(batch, opcode)
	if complete {
		t.Fatal("malformed stream completed")
	}
	if err == nil {
		t.Fatal("malformed stream returned no error")
	}
}

func TestReportsReadAddressInWrittenOrder(t *testing.T) {
	// 7c:2f:80:1a:2b:3c, a resolvable private address, as an iPhone advertises Apple's nearby info.
	data := []byte{0x02, 0x01, 0x1a, 0x0b, 0xff, 0x4c, 0x00, 0x10, 0x06, 0x41, 0x1e, 0xf4, 0x2c, 0x9a, 0xd8}
	report := []byte{h4Event, evtLEMeta, 0, leAdvertisingReport, 1, 0x00, 0x01,
		0x3c, 0x2b, 0x1a, 0x80, 0x2f, 0x7c, byte(len(data))}
	report = append(report, data...)
	report = append(report, 0xc4) // RSSI -60
	report[2] = byte(len(report) - 3)

	var got []Advertisement
	r := &Radio{found: func(a Advertisement) { got = append(got, a) }}
	remainder, err := r.parse(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(remainder) != 0 {
		t.Errorf("remainder = %x, want empty", remainder)
	}
	if len(got) != 1 {
		t.Fatalf("advertisements = %d, want 1", len(got))
	}

	a := got[0]
	if a.Addr() != 0x7c2f801a2b3c {
		t.Errorf("address = %012x, want 7c2f801a2b3c", a.Addr())
	}
	if a.AddressType != 0x01 {
		t.Errorf("address type = %d, want 1", a.AddressType)
	}
	if a.RSSI != -60 {
		t.Errorf("rssi = %d, want -60", a.RSSI)
	}
	if !slices.Equal(a.Data, data) {
		t.Errorf("data = %x, want %x", a.Data, data)
	}
}

func commandComplete(opcode uint16, status byte) []byte {
	event := []byte{h4Event, evtCommandComplete, 4, 1, 0, 0, status}
	binary.LittleEndian.PutUint16(event[4:], opcode)
	return event
}
