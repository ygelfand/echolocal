package alsa

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"
	"unsafe"
)

// Control structure layouts. The ioctl number encodes each struct's size, so these have to match
// the kernel's exactly or the call is rejected outright.
//
//	snd_ctl_elem_id     numid, iface, device, subdevice, name[44], index       = 64
//	snd_ctl_elem_info   id, type, access, count, owner, value[128], dimen[8],
//	                    reserved[56]                                           = 272
//	snd_ctl_elem_value  id, indirect, pad, value[128 longs], tstamp, reserved   = 1224
//
// The value union holds a long long array, so it is 8-byte aligned and the word before it is
// padded to match. snd_ctl_elem_info is the same size either way, because its own value union is
// dominated by a 128-byte reserved member; snd_ctl_elem_value is not, because it stores integers
// as an array of longs, and its trailing timespec is word sized too.
const (
	elemIDSize   = 64
	elemInfoSize = 272

	elemValueSize = valueDataOff + 128*longSize + 128 // 1224

	idNumidOff = 0
	idNameOff  = 16
	idNameLen  = 44

	infoTypeOff  = elemIDSize
	infoCountOff = elemIDSize + 8
	// The value union, which the control's type selects. For an integer control it is min, max and
	// step; for an enumerated one, items, item and name[64].
	infoRangeOff     = elemIDSize + 16
	infoEnumItemsOff = elemIDSize + 16
	infoEnumItemOff  = infoEnumItemsOff + 4
	infoEnumNameOff  = infoEnumItemsOff + 8
	infoEnumNameLen  = 64

	valueDataOff = elemIDSize + 8
)

// Control types.
const (
	TypeBoolean    = 1
	TypeInteger    = 2
	TypeEnumerated = 3
	TypeBytes      = 4
	TypeInteger64  = 5
)

var (
	ioctlElemInfo  = ioc(3, 'U', 0x11, elemInfoSize)
	ioctlElemRead  = ioc(3, 'U', 0x12, elemValueSize)
	ioctlElemWrite = ioc(3, 'U', 0x13, elemValueSize)
)

// Mixer is an open control device.
type Mixer struct {
	f *os.File

	mu     sync.Mutex
	cached []Control
}

// OpenMixer opens a card's control device. Enumeration happens on first use and is cached: it
// costs over a second, so callers should not do it on a path where something is waiting.
func OpenMixer(card int) (*Mixer, error) {
	f, err := os.OpenFile(fmt.Sprintf("/dev/snd/controlC%d", card), os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	return &Mixer{f: f}, nil
}

func (m *Mixer) Close() error { return m.f.Close() }

// Control is one mixer control.
type Control struct {
	Numid uint32
	Name  string
	Type  uint32
	Count uint32
	Items []string

	// The range of an integer control, zero for any other type.
	Min, Max, Step int64
}

// Controls lists the card's controls. There is no cheap way to ask how many there are, so this
// walks numids until the kernel stops recognising them.
//
// The walk costs one ioctl per control plus one per item name of every enumerated control, which
// is over a second on this card, so the result is kept: the set does not change while the card
// is open.
func (m *Mixer) Controls() ([]Control, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cached != nil {
		return m.cached, nil
	}

	var out []Control
	for numid := uint32(1); ; numid++ {
		c, err := m.info(numid)
		if err != nil {
			// The first gap is the end: numids are dense on a card that is not being
			// reconfigured underneath us.
			break
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("alsa: no controls on the card")
	}
	m.cached = out
	return out, nil
}

// Find looks a control up by name.
func (m *Mixer) Find(name string) (Control, error) {
	controls, err := m.Controls()
	if err != nil {
		return Control{}, err
	}
	for _, c := range controls {
		if c.Name == name {
			return c, nil
		}
	}
	return Control{}, fmt.Errorf("alsa: no control named %q", name)
}

// info reads a control's metadata, including the names of an enumerated control's items.
func (m *Mixer) info(numid uint32) (Control, error) {
	var buf [elemInfoSize]byte
	binary.LittleEndian.PutUint32(buf[idNumidOff:], numid)
	if err := ioctl(m.f.Fd(), ioctlElemInfo, unsafe.Pointer(&buf)); err != nil {
		return Control{}, err
	}

	c := Control{
		Numid: numid,
		Name:  cstr(buf[idNameOff : idNameOff+idNameLen]),
		Type:  binary.LittleEndian.Uint32(buf[infoTypeOff:]),
		Count: binary.LittleEndian.Uint32(buf[infoCountOff:]),
	}
	// The value union that follows the count is the type's own: min, max and step for an integer
	// control, item names for an enumerated one.
	switch c.Type {
	case TypeInteger:
		c.Min = int64(int32(binary.LittleEndian.Uint32(buf[infoRangeOff:])))
		c.Max = int64(int32(binary.LittleEndian.Uint32(buf[infoRangeOff+longSize:])))
		c.Step = int64(int32(binary.LittleEndian.Uint32(buf[infoRangeOff+2*longSize:])))
		return c, nil
	case TypeInteger64:
		c.Min = int64(binary.LittleEndian.Uint64(buf[infoRangeOff:]))
		c.Max = int64(binary.LittleEndian.Uint64(buf[infoRangeOff+8:]))
		c.Step = int64(binary.LittleEndian.Uint64(buf[infoRangeOff+16:]))
		return c, nil
	case TypeEnumerated:
	default:
		return c, nil
	}

	// Item names come one per call: set the index, ask again, read the name back.
	items := binary.LittleEndian.Uint32(buf[infoEnumItemsOff:])
	for i := uint32(0); i < items; i++ {
		var q [elemInfoSize]byte
		binary.LittleEndian.PutUint32(q[idNumidOff:], numid)
		binary.LittleEndian.PutUint32(q[infoEnumItemOff:], i)
		if err := ioctl(m.f.Fd(), ioctlElemInfo, unsafe.Pointer(&q)); err != nil {
			return c, err
		}
		c.Items = append(c.Items, cstr(q[infoEnumNameOff:infoEnumNameOff+infoEnumNameLen]))
	}
	return c, nil
}

// Get reads a control's values.
func (m *Mixer) Get(c Control) ([]uint32, error) {
	var buf [elemValueSize]byte
	binary.LittleEndian.PutUint32(buf[idNumidOff:], c.Numid)
	if err := ioctl(m.f.Fd(), ioctlElemRead, unsafe.Pointer(&buf)); err != nil {
		return nil, fmt.Errorf("alsa: reading %q: %w", c.Name, err)
	}

	w := valueWidth(c.Type)
	out := make([]uint32, c.Count)
	for i := range out {
		if w == 1 {
			out[i] = uint32(buf[valueDataOff+i])
			continue
		}
		out[i] = binary.LittleEndian.Uint32(buf[valueDataOff+i*w:])
	}
	return out, nil
}

// valueWidth is the stride between a control's values in snd_ctl_elem_value, which is whichever
// member of the union the control's type selects. Reading an integer control with a 32-bit stride
// returns the high half of the previous value instead of the next one.
func valueWidth(typ uint32) int {
	switch typ {
	case TypeEnumerated:
		return 4 // unsigned int item[128]
	case TypeBytes:
		return 1 // unsigned char data[512]
	}
	return longSize // long value[128], and long long for TypeInteger64
}

// Set writes the same value to every channel of a control.
func (m *Mixer) Set(c Control, v uint32) error {
	var buf [elemValueSize]byte
	binary.LittleEndian.PutUint32(buf[idNumidOff:], c.Numid)
	w := uint32(valueWidth(c.Type))
	for i := uint32(0); i < c.Count; i++ {
		binary.LittleEndian.PutUint32(buf[valueDataOff+i*w:], v)
	}
	if err := ioctl(m.f.Fd(), ioctlElemWrite, unsafe.Pointer(&buf)); err != nil {
		return fmt.Errorf("alsa: writing %q: %w", c.Name, err)
	}
	return nil
}

// SetAll writes one value per channel, for controls that carry a different value in each: a
// coefficient blob rather than a level.
func (m *Mixer) SetAll(c Control, values []uint32) error {
	if uint32(len(values)) != c.Count {
		return fmt.Errorf("alsa: %q takes %d values, given %d", c.Name, c.Count, len(values))
	}

	var buf [elemValueSize]byte
	binary.LittleEndian.PutUint32(buf[idNumidOff:], c.Numid)
	w := valueWidth(c.Type)
	for i, v := range values {
		if w == 1 {
			buf[valueDataOff+i] = byte(v)
			continue
		}
		binary.LittleEndian.PutUint32(buf[valueDataOff+i*w:], v)
	}
	if err := ioctl(m.f.Fd(), ioctlElemWrite, unsafe.Pointer(&buf)); err != nil {
		return fmt.Errorf("alsa: writing %q: %w", c.Name, err)
	}
	return nil
}

// SetBytes writes a byte-typed control, by name.
func (m *Mixer) SetBytes(name string, data []byte) error {
	c, err := m.Find(name)
	if err != nil {
		return err
	}
	if c.Type != TypeBytes {
		return fmt.Errorf("alsa: %q is not a byte control", name)
	}

	values := make([]uint32, len(data))
	for i, b := range data {
		values[i] = uint32(b)
	}
	return m.SetAll(c, values)
}

// SetEnum picks an enumerated control's item by name.
func (m *Mixer) SetEnum(name, item string) error {
	c, err := m.Find(name)
	if err != nil {
		return err
	}
	if c.Type != TypeEnumerated {
		return fmt.Errorf("alsa: %q is not an enumerated control", name)
	}
	for i, have := range c.Items {
		if strings.EqualFold(have, item) {
			return m.Set(c, uint32(i))
		}
	}
	return fmt.Errorf("alsa: %q has no item %q, only %v", name, item, c.Items)
}

// SetInt writes a value to an integer control, by name.
func (m *Mixer) SetInt(name string, v uint32) error {
	c, err := m.Find(name)
	if err != nil {
		return err
	}
	if c.Type == TypeEnumerated {
		return fmt.Errorf("alsa: %q is an enumerated control", name)
	}
	return m.Set(c, v)
}

// GetEnum reports an enumerated control's current item.
func (m *Mixer) GetEnum(name string) (string, error) {
	c, err := m.Find(name)
	if err != nil {
		return "", err
	}
	v, err := m.Get(c)
	if err != nil {
		return "", err
	}
	if len(v) == 0 || int(v[0]) >= len(c.Items) {
		return "", fmt.Errorf("alsa: %q reports item %v, outside %v", name, v, c.Items)
	}
	return c.Items[v[0]], nil
}

func cstr(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}
