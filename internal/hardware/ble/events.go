package ble

import (
	"encoding/binary"
	"fmt"
)

const (
	h4Event = 0x04

	evtCommandComplete  = 0x0E
	evtCommandStatus    = 0x0F
	evtLEMeta           = 0x3E
	leAdvertisingReport = 0x02
)

// nextEvent removes one complete H4 event from the front. Remainder is the partial packet to hold,
// or nil when the stream is malformed.
func nextEvent(b []byte) (event, remainder []byte, ok bool, err error) {
	if len(b) < 3 {
		return nil, b, false, nil
	}
	if b[0] != h4Event {
		return nil, nil, false, fmt.Errorf("unexpected H4 packet type 0x%02x", b[0])
	}
	size := 3 + int(b[2])
	if len(b) < size {
		return nil, b, false, nil
	}
	return b[:size], b[size:], true, nil
}

// commandResult consumes complete events until it finds the matching Command Complete.
func commandResult(b []byte, opcode uint16) (remainder []byte, complete bool, err error) {
	for {
		event, rest, ok, err := nextEvent(b)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return append([]byte(nil), rest...), false, nil
		}
		b = rest

		switch event[1] {
		case evtCommandComplete:
			if len(event) < 7 {
				return nil, false, fmt.Errorf("short Command Complete event: %x", event)
			}
			if binary.LittleEndian.Uint16(event[4:]) != opcode {
				continue
			}
			if status := event[6]; status != 0 {
				return append([]byte(nil), b...), true, fmt.Errorf("status 0x%02x", status)
			}
			return append([]byte(nil), b...), true, nil
		case evtCommandStatus:
			if len(event) < 7 {
				return nil, false, fmt.Errorf("short Command Status event: %x", event)
			}
			if binary.LittleEndian.Uint16(event[5:]) != opcode {
				continue
			}
			if status := event[3]; status != 0 {
				return append([]byte(nil), b...), true, fmt.Errorf("status 0x%02x", status)
			}
		}
	}
}
