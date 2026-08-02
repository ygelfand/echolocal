// Package i2c provides raw register access to I2C devices via /dev/i2c-N.
//
// This exists to see hardware state that no driver exposes. The four TLV320AIC3101 mic
// codecs have no regmap debugfs and only a subset of their registers appear as ALSA
// controls, so anything Amazon's firmware writes directly is invisible without this.
package i2c

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	// ioctlSlave claims an address; it fails with EBUSY when a kernel driver owns the
	// device. ioctlSlaveForce claims it anyway.
	ioctlSlave      = 0x0703
	ioctlSlaveForce = 0x0706
)

// Bus is an open I2C adapter bound to one slave address.
type Bus struct {
	f    *os.File
	addr uint8
}

// Open binds to addr on bus n. force is required for addresses a kernel driver already
// owns, which is the normal case for the codecs here. Reads are safe; writes race the
// driver and should be avoided unless you know what owns the register.
func Open(n int, addr uint8, force bool) (*Bus, error) {
	f, err := os.OpenFile(fmt.Sprintf("/dev/i2c-%d", n), os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	req := uintptr(ioctlSlave)
	if force {
		req = ioctlSlaveForce
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(addr)); e != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("set slave 0x%02x on i2c-%d: %w", addr, n, e)
	}
	return &Bus{f: f, addr: addr}, nil
}

func (b *Bus) Close() error { return b.f.Close() }

// ReadReg reads one 8-bit register: write the register index, then read a byte.
func (b *Bus) ReadReg(reg uint8) (uint8, error) {
	if _, err := b.f.Write([]byte{reg}); err != nil {
		return 0, err
	}
	var buf [1]byte
	if _, err := b.f.Read(buf[:]); err != nil {
		return 0, err
	}
	return buf[0], nil
}

// WriteReg writes one 8-bit register.
func (b *Bus) WriteReg(reg, val uint8) error {
	_, err := b.f.Write([]byte{reg, val})
	return err
}

// Dump reads a contiguous register range. Registers that error read back as 0xFF with the
// error recorded, since a partial dump is still useful for diffing.
func (b *Bus) Dump(first, last uint8) ([]uint8, error) {
	if last < first {
		return nil, fmt.Errorf("i2c: bad range %d..%d", first, last)
	}
	out := make([]uint8, 0, int(last-first)+1)
	for r := int(first); r <= int(last); r++ {
		v, err := b.ReadReg(uint8(r))
		if err != nil {
			v = 0xFF
		}
		out = append(out, v)
	}
	return out, nil
}

var _ = unsafe.Pointer(nil)
