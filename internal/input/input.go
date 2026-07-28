// Package input reads Linux evdev devices directly, with no cgo and no getevent.
package input

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Event types we care about.
const (
	EvSyn = 0x00
	EvKey = 0x01
	EvRel = 0x02
	EvAbs = 0x03
	EvSw  = 0x05
	EvRep = 0x14
)

// sizeof(struct input_event): a timeval of two kernel longs, then u16 type, u16 code, s32 value,
// with the tail padded back out to the timeval's alignment. evdev rejects a read shorter than one
// whole event with EINVAL rather than returning a truncated one, so this has to be right.
const (
	longSize  = 8
	eventSize = 2*longSize + 8

	evType  = 2 * longSize
	evCode  = evType + 2
	evValue = evCode + 2
)

// Event is one evdev record. The timestamp is kept at the width the kernel reports it.
type Event struct {
	Sec, Usec uint64
	Type      uint16
	Code      uint16
	Value     int32
}

func (e Event) TypeName() string {
	switch e.Type {
	case EvSyn:
		return "SYN"
	case EvKey:
		return "KEY"
	case EvRel:
		return "REL"
	case EvAbs:
		return "ABS"
	case EvSw:
		return "SW"
	case EvRep:
		return "REP"
	}
	return fmt.Sprintf("TYPE_%d", e.Type)
}

func (e Event) CodeName() string {
	if e.Type != EvKey {
		return fmt.Sprintf("%d", e.Code)
	}
	return fmt.Sprintf("KEY_%d", e.Code)
}

func (e Event) String() string {
	action := ""
	if e.Type == EvKey {
		switch e.Value {
		case 0:
			action = " release"
		case 1:
			action = " press"
		case 2:
			action = " repeat"
		}
	}
	return fmt.Sprintf("%-4s %-16s value=%d%s", e.TypeName(), e.CodeName(), e.Value, action)
}

// Device is an open evdev node.
type Device struct {
	Path string
	Name string
	f    *os.File
}

// Open opens one event node and reads its reported name.
func Open(path string) (*Device, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &Device{Path: path, Name: nameFor(path), f: f}, nil
}

// nameFor reads the device name from sysfs, which avoids an EVIOCGNAME ioctl.
func nameFor(path string) string {
	base := filepath.Base(path)
	b, err := os.ReadFile("/sys/class/input/" + base + "/device/name")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}

// Read blocks for the next event.
func (d *Device) Read() (Event, error) {
	var buf [eventSize]byte
	if _, err := readFull(d.f, buf[:]); err != nil {
		return Event{}, err
	}
	return Event{
		Sec:   binary.LittleEndian.Uint64(buf[0:]),
		Usec:  binary.LittleEndian.Uint64(buf[longSize:]),
		Type:  binary.LittleEndian.Uint16(buf[evType:]),
		Code:  binary.LittleEndian.Uint16(buf[evCode:]),
		Value: int32(binary.LittleEndian.Uint32(buf[evValue:])),
	}, nil
}

func (d *Device) Close() error { return d.f.Close() }

func readFull(f *os.File, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := f.Read(b[n:])
		if err != nil {
			return n, err
		}
		n += m
	}
	return n, nil
}

// List returns every /dev/input/event* node with its name.
func List() ([]*Device, error) {
	paths, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		return nil, err
	}
	var out []*Device
	for _, p := range paths {
		d, err := Open(p)
		if err != nil {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}
