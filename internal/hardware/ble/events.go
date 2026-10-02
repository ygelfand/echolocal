package ble

import (
	"encoding/binary"
	"fmt"
)

const (
	h4ACL   = 0x02
	h4Event = 0x04

	evtDisconnectionComplete = 0x05
	evtEncryptionChange      = 0x08
	evtKeyRefreshComplete    = 0x30
	evtCommandComplete       = 0x0E
	evtCommandStatus         = 0x0F
	evtNumCompletedPackets   = 0x13
	evtLEMeta                = 0x3E

	leConnectionComplete         = 0x01
	leAdvertisingReport          = 0x02
	leLongTermKeyRequest         = 0x05
	leRemoteConnParamRequest     = 0x06
	leEnhancedConnectionComplete = 0x0A
)

// nextEvent removes one complete H4 packet from the front: an event, or ACL data once a connection
// is up. Remainder is the partial packet to hold, or nil when the stream is malformed.
func nextEvent(b []byte) (event, remainder []byte, ok bool, err error) {
	if len(b) == 0 {
		return nil, b, false, nil
	}
	var size int
	switch b[0] {
	case h4Event:
		if len(b) < 3 {
			return nil, b, false, nil
		}
		size = 3 + int(b[2])
	case h4ACL:
		if len(b) < 5 {
			return nil, b, false, nil
		}
		size = 5 + int(binary.LittleEndian.Uint16(b[3:]))
	default:
		return nil, nil, false, fmt.Errorf("unexpected H4 packet type 0x%02x", b[0])
	}
	if len(b) < size {
		return nil, b, false, nil
	}
	return b[:size], b[size:], true, nil
}

// commandResult consumes complete events until it finds the matching Command Complete, and returns
// its return parameters after the status.
func commandResult(b []byte, opcode uint16) (remainder, params []byte, complete bool, err error) {
	for {
		event, rest, ok, err := nextEvent(b)
		if err != nil {
			return nil, nil, false, err
		}
		if !ok {
			return append([]byte(nil), rest...), nil, false, nil
		}
		b = rest
		if event[0] != h4Event {
			continue
		}

		done, params, err := completes(event, opcode)
		if done || err != nil {
			return append([]byte(nil), b...), params, true, err
		}
	}
}

// completes reports whether event finishes the command opcode: a Command Complete, or a failing
// Command Status. A successful Command Status only acknowledges; what follows is its own event.
func completes(event []byte, opcode uint16) (done bool, params []byte, err error) {
	switch event[1] {
	case evtCommandComplete:
		if len(event) < 7 {
			return false, nil, fmt.Errorf("short Command Complete event: %x", event)
		}
		if binary.LittleEndian.Uint16(event[4:]) != opcode {
			return false, nil, nil
		}
		if status := event[6]; status != 0 {
			return true, nil, StatusError(status)
		}
		return true, append([]byte(nil), event[7:]...), nil
	case evtCommandStatus:
		if len(event) < 7 {
			return false, nil, fmt.Errorf("short Command Status event: %x", event)
		}
		if binary.LittleEndian.Uint16(event[5:]) != opcode {
			return false, nil, nil
		}
		if status := event[3]; status != 0 {
			return true, nil, StatusError(status)
		}
	}
	return false, nil, nil
}

// StatusError is an HCI status code: a failed command, or why a connection ended.
type StatusError uint8

func (s StatusError) Error() string { return fmt.Sprintf("status 0x%02x", uint8(s)) }
