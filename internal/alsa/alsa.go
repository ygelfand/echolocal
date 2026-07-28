// Package alsa captures PCM audio by driving the /dev/snd ioctl interface directly.
//
// This exists instead of binding tinyalsa through cgo. The kernel ioctl ABI is stable and
// the subset a capture-only client needs is small, so the whole thing is a few hundred
// lines of Go and echod stays a static, cgo-free binary — no NDK image, no C toolchain.
//
// Several of these structures embed the kernel's unsigned long, so their size — and with it the
// ioctl number, which encodes that size — depends on the word size. The layouts below derive from
// it rather than assuming one, because a mismatch is not a subtle bug: the kernel rejects the call
// outright, or worse, reads the wrong bytes.
package alsa

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// longSize is the kernel's unsigned long. These layouts are the 64-bit ABI.
const longSize = 8

// struct snd_pcm_hw_params: flags, 8 masks of 32 bytes, 21 intervals of 12, six 32-bit scalars,
// fifo_size as an unsigned long, reserved[64]. Only fifo_size is word sized, so all the offsets
// hold on either ABI and only the total differs: 604 on 32-bit, 608 here.
const (
	maskOff     = 4
	maskSize    = 32
	intervalOff = maskOff + 8*maskSize // 260
	intervalLen = 12
	rmaskOff    = intervalOff + 21*intervalLen // 512
	infoOff     = rmaskOff + 8

	hwParamsSize = rmaskOff + 6*4 + longSize + 64 // 608
)

// Parameter indices. Masks are 0..2, intervals 8..19.
const (
	paramAccess    = 0
	paramFormat    = 1
	paramSubformat = 2
	firstMask      = paramAccess
	lastMask       = paramSubformat

	paramSampleBits = 8
	paramFrameBits  = 9
	paramChannels   = 10
	paramRate       = 11
	paramPeriodSize = 13
	paramPeriods    = 15
	firstInterval   = paramSampleBits
	lastInterval    = 19
)

const (
	accessRWInterleaved = 3
	subformatStd        = 0

	// FormatS24_3LE is 24 bits packed into 3 bytes — the only format the Echo Dot's
	// capture codec accepts.
	FormatS24_3LE = 32
)

// ioc builds an ioctl request number the same way asm-generic/ioctl.h does.
func ioc(dir, typ, nr, size uintptr) uintptr {
	return dir<<30 | size<<16 | typ<<8 | nr
}

var (
	ioctlHwParams = ioc(3, 'A', 0x11, hwParamsSize)
	ioctlPrepare  = ioc(0, 'A', 0x40, 0)
	ioctlStart    = ioc(0, 'A', 0x42, 0)
	ioctlDrop     = ioc(0, 'A', 0x43, 0)
	ioctlReadi    = ioc(2, 'A', 0x51, xferiSize)
)

type hwParams [hwParamsSize]byte

func (p *hwParams) set(off int, v uint32) { binary.LittleEndian.PutUint32(p[off:], v) }
func (p *hwParams) get(off int) uint32    { return binary.LittleEndian.Uint32(p[off:]) }

// init opens every parameter to its full range so the driver can narrow them.
func (p *hwParams) init() {
	for i := range p {
		p[i] = 0
	}
	for m := firstMask; m <= lastMask; m++ {
		off := maskOff + m*maskSize
		p.set(off, ^uint32(0))
		p.set(off+4, ^uint32(0))
	}
	for n := firstInterval; n <= lastInterval; n++ {
		off := intervalOff + (n-firstInterval)*intervalLen
		p.set(off, 0)
		p.set(off+4, ^uint32(0))
	}
	p.set(rmaskOff, ^uint32(0))
	p.set(infoOff, ^uint32(0))
}

func (p *hwParams) setMask(param, bit int) {
	off := maskOff + param*maskSize
	for i := 0; i < maskSize/4; i++ {
		p.set(off+i*4, 0)
	}
	p.set(off+(bit>>5)*4, 1<<uint(bit&31))
}

// setInterval pins a parameter to one value. Bit 2 of the flags word is "integer".
func (p *hwParams) setInterval(param int, v uint32) {
	off := intervalOff + (param-firstInterval)*intervalLen
	p.set(off, v)
	p.set(off+4, v)
	p.set(off+8, 1<<2)
}

func (p *hwParams) intervalMin(param int) uint32 {
	return p.get(intervalOff + (param-firstInterval)*intervalLen)
}

// snd_xferi: a signed long, a pointer and an unsigned long, so 24 bytes here.
type xferi struct {
	result int64
	buf    uintptr
	frames uintptr
}

const xferiSize = 3 * longSize // 24

type Config struct {
	Channels   int
	Rate       int
	Format     int
	Bits       int // physical bits per sample: 24 for S24_3LE
	PeriodSize int
	Periods    int
}

// Capture is an open PCM capture stream.
type Capture struct {
	f          *os.File
	cfg        Config
	frameBytes int
}

func ioctl(fd, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

func ioctlArgless(fd, req uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, 0); e != 0 {
		return e
	}
	return nil
}

// Open configures and starts a capture stream. The open is non-blocking so a device someone else
// holds returns ErrBusy instead of waiting; reads block normally once it is ours.
func Open(card, device int, cfg Config) (*Capture, error) {
	path := fmt.Sprintf("/dev/snd/pcmC%dD%dc", card, device)
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w: %s", ErrBusy, path)
		}
		return nil, err
	}
	if err := clearNonBlock(f); err != nil {
		_ = f.Close()
		return nil, err
	}

	var p hwParams
	p.init()
	p.setMask(paramAccess, accessRWInterleaved)
	p.setMask(paramFormat, cfg.Format)
	p.setMask(paramSubformat, subformatStd)
	p.setInterval(paramSampleBits, uint32(cfg.Bits))
	p.setInterval(paramFrameBits, uint32(cfg.Bits*cfg.Channels))
	p.setInterval(paramChannels, uint32(cfg.Channels))
	p.setInterval(paramRate, uint32(cfg.Rate))
	p.setInterval(paramPeriodSize, uint32(cfg.PeriodSize))
	p.setInterval(paramPeriods, uint32(cfg.Periods))

	if err := ioctl(f.Fd(), ioctlHwParams, unsafe.Pointer(&p)); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("hw_params: %w", err)
	}
	if err := ioctlArgless(f.Fd(), ioctlPrepare); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("prepare: %w", err)
	}
	if err := ioctlArgless(f.Fd(), ioctlStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("start: %w", err)
	}

	return &Capture{
		f:          f,
		cfg:        cfg,
		frameBytes: cfg.Channels * cfg.Bits / 8,
	}, nil
}

// FrameBytes is the size of one interleaved frame across all channels.
func (c *Capture) FrameBytes() int { return c.frameBytes }

// Read fills buf with whole frames and returns the number of bytes read. On overrun the
// stream is re-prepared and restarted, and the call reports how many bytes were lost.
func (c *Capture) Read(buf []byte) (int, error) {
	frames := len(buf) / c.frameBytes
	if frames == 0 {
		return 0, fmt.Errorf("buffer smaller than one frame (%d bytes)", c.frameBytes)
	}
	x := xferi{
		buf:    uintptr(unsafe.Pointer(&buf[0])),
		frames: uintptr(frames),
	}
	if err := ioctl(c.f.Fd(), ioctlReadi, unsafe.Pointer(&x)); err != nil {
		if err == syscall.EPIPE {
			_ = ioctlArgless(c.f.Fd(), ioctlPrepare)
			_ = ioctlArgless(c.f.Fd(), ioctlStart)
			return 0, ErrOverrun
		}
		return 0, err
	}
	return int(x.result) * c.frameBytes, nil
}

func (c *Capture) Close() error {
	_ = ioctlArgless(c.f.Fd(), ioctlDrop)
	return c.f.Close()
}

// ErrOverrun means the hardware ring wrapped before we read it; audio was lost.
var ErrOverrun = fmt.Errorf("alsa: capture overrun")
