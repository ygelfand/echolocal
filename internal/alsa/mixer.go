package alsa

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"
	"unsafe"
)

// Control structure layouts, 32-bit userspace. The ioctl number encodes each struct's size, so
// these have to match the kernel's exactly or the call is rejected outright.
//
//	snd_ctl_elem_id     numid, iface, device, subdevice, name[44], index      = 64
//	snd_ctl_elem_info   id, type, access, count, owner, value[128], dimen[8],
//	                    reserved[56]                                          = 272
//	snd_ctl_elem_value  id, indirect, pad, value[512], tstamp[8], reserved[120] = 712
//
// The value union holds a long long array, so it is 8-byte aligned and the word before it is
// padded to match.
const (
	elemIDSize    = 64
	elemInfoSize  = 272
	elemValueSize = 712

	idNumidOff = 0
	idNameOff  = 16
	idNameLen  = 44

	infoTypeOff  = elemIDSize
	infoCountOff = elemIDSize + 8
	// The value union for an enumerated control: items, item, name[64].
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
	if c.Type != TypeEnumerated {
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

	out := make([]uint32, c.Count)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(buf[valueDataOff+i*4:])
	}
	return out, nil
}

// Set writes the same value to every channel of a control.
func (m *Mixer) Set(c Control, v uint32) error {
	var buf [elemValueSize]byte
	binary.LittleEndian.PutUint32(buf[idNumidOff:], c.Numid)
	for i := uint32(0); i < c.Count; i++ {
		binary.LittleEndian.PutUint32(buf[valueDataOff+i*4:], v)
	}
	if err := ioctl(m.f.Fd(), ioctlElemWrite, unsafe.Pointer(&buf)); err != nil {
		return fmt.Errorf("alsa: writing %q: %w", c.Name, err)
	}
	return nil
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
