package alsa

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// FormatS16_LE is 16-bit little endian, the only format the playback codec accepts.
const FormatS16_LE = 2

var (
	ioctlWritei = ioc(1, 'A', 0x50, 12)
	ioctlDrain  = ioc(0, 'A', 0x44, 0)
)

// Playback is an open PCM playback stream.
type Playback struct {
	f          *os.File
	cfg        Config
	frameBytes int
	started    bool
}

// OpenPlayback configures a playback stream. The hardware starts itself once the buffer is full.
//
// The open is non-blocking: mediaserver holds this device, and a blocking open waits forever.
// A held device returns ErrBusy. Writes block normally once the device is ours.
func OpenPlayback(card, device int, cfg Config) (*Playback, error) {
	path := fmt.Sprintf("/dev/snd/pcmC%dD%dp", card, device)
	f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w: %s", ErrBusy, path)
		}
		return nil, err
	}

	// Writes should block; only the open needed to fail fast.
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

	return &Playback{
		f:          f,
		cfg:        cfg,
		frameBytes: cfg.Channels * cfg.Bits / 8,
	}, nil
}

// FrameBytes is the size of one interleaved frame across all channels.
func (p *Playback) FrameBytes() int { return p.frameBytes }

// Write plays whole frames, blocking until the hardware has room. On underrun the stream is
// re-prepared and the call reports it, since a gap in playback is worth knowing about.
func (p *Playback) Write(buf []byte) (int, error) {
	frames := len(buf) / p.frameBytes
	if frames == 0 {
		return 0, fmt.Errorf("buffer smaller than one frame (%d bytes)", p.frameBytes)
	}
	x := xferi{
		buf:    uintptr(unsafe.Pointer(&buf[0])),
		frames: uintptr(frames),
	}
	if err := ioctl(p.f.Fd(), ioctlWritei, unsafe.Pointer(&x)); err != nil {
		if err == syscall.EPIPE {
			_ = ioctlArgless(p.f.Fd(), ioctlPrepare)
			p.started = false
			return 0, ErrUnderrun
		}
		return 0, err
	}

	if !p.started {
		p.started = true
		// EBADFD if the stream is already running, which is not an error here.
		if err := ioctlArgless(p.f.Fd(), ioctlStart); err != nil && err != syscall.Errno(0x4d) {
			return int(x.result) * p.frameBytes, fmt.Errorf("start: %w", err)
		}
	}
	return int(x.result) * p.frameBytes, nil
}

// Drain waits for buffered audio to finish playing.
func (p *Playback) Drain() error { return ioctlArgless(p.f.Fd(), ioctlDrain) }

func (p *Playback) Close() error {
	_ = ioctlArgless(p.f.Fd(), ioctlDrop)
	return p.f.Close()
}

// ErrUnderrun means the hardware ran out of samples; playback gapped.
var ErrUnderrun = fmt.Errorf("alsa: playback underrun")

// ErrBusy means another process holds the device.
var ErrBusy = errors.New("alsa: device busy")

// clearNonBlock puts the descriptor back into blocking mode.
func clearNonBlock(f *os.File) error {
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_GETFL, 0)
	if errno != 0 {
		return fmt.Errorf("alsa: reading descriptor flags: %w", errno)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_SETFL, flags&^syscall.O_NONBLOCK); errno != 0 {
		return fmt.Errorf("alsa: clearing O_NONBLOCK: %w", errno)
	}
	return nil
}
