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

// eventSize is sizeof(struct input_event) on 32-bit Linux: two longs of timeval, then
// u16 type, u16 code, s32 value.
const eventSize = 16

// Event is one evdev record.
type Event struct {
	Sec, Usec uint32
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

// CodeName maps the key codes this hardware actually emits. Anything unmapped prints its
// number, which is what matters when discovering a new button.
func (e Event) CodeName() string {
	if e.Type != EvKey {
		return fmt.Sprintf("%d", e.Code)
	}
	if n, ok := keyNames[e.Code]; ok {
		return n
	}
	return fmt.Sprintf("KEY_%d", e.Code)
}

var keyNames = map[uint16]string{
	113: "KEY_MUTE",
	114: "KEY_VOLUMEDOWN",
	115: "KEY_VOLUMEUP",
	116: "KEY_POWER",
	139: "KEY_MENU",
	158: "KEY_BACK",
	172: "KEY_HOMEPAGE",
	212: "KEY_CAMERA",
	217: "KEY_SEARCH",
	528: "KEY_FOCUS",
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
		Sec:   binary.LittleEndian.Uint32(buf[0:]),
		Usec:  binary.LittleEndian.Uint32(buf[4:]),
		Type:  binary.LittleEndian.Uint16(buf[8:]),
		Code:  binary.LittleEndian.Uint16(buf[10:]),
		Value: int32(binary.LittleEndian.Uint32(buf[12:])),
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
