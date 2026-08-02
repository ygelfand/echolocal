// Package ble scans for Bluetooth Low Energy advertisements over the controller's HCI node.
package ble

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"syscall"
)

// Node is the controller's HCI character device.
const Node = "/dev/stpbt"

const (
	h4Command = 0x01
	h4Event   = 0x04

	evtCommandComplete  = 0x0E
	evtLEMeta           = 0x3E
	leAdvertisingReport = 0x02

	cmdReset        = 0x0C03
	cmdLEScanParams = 0x200B
	cmdLEScanEnable = 0x200C
)

// How much of the time the radio listens, in units of 0.625 ms: 18.75 ms out of every 200 ms. Wifi
// and Bluetooth share one antenna here, so a window equal to the interval never gives it back.
const (
	scanInterval = 320
	scanWindow   = 30
)

// Advertisement is one LE advertising report.
type Advertisement struct {
	Address     [6]byte
	AddressType uint8
	RSSI        int8
	Data        []byte
}

// Addr is the address as a big-endian integer.
func (a Advertisement) Addr() uint64 {
	var v uint64
	for _, b := range a.Address {
		v = v<<8 | uint64(b)
	}
	return v
}

// Radio is the controller. One node, one owner.
type Radio struct {
	mu       sync.Mutex
	fd       int
	open     bool
	stop     context.CancelFunc
	finished chan struct{}

	reports uint64
}

var (
	once  sync.Once
	radio *Radio
)

// Get is the radio.
func Get() *Radio {
	once.Do(func() { radio = &Radio{fd: -1} })
	return radio
}

// Scanning reports whether a scan is running.
func (r *Radio) Scanning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.open
}

// Reports is how many LE events have arrived.
func (r *Radio) Reports() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reports
}

// Start opens the controller and scans until Stop. Reports arrive on the reader's goroutine. Active
// asks for scan responses, which transmits rather than only listening.
func (r *Radio) Start(active bool, found func(Advertisement)) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.open {
		return nil
	}

	// A blocking open never returns: the driver powers the chip on inside it.
	fd, err := syscall.Open(Node, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("ble: opening %s: %w", Node, err)
	}
	if err := clearNonBlock(fd); err != nil {
		_ = syscall.Close(fd)
		return err
	}
	r.fd = fd

	if err := r.begin(active); err != nil {
		_ = syscall.Close(fd)
		r.fd = -1
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.stop, r.finished, r.open = cancel, make(chan struct{}), true

	go r.read(ctx, found)
	slog.Info("ble scanning")
	return nil
}

// Stop ends the scan and closes the node.
func (r *Radio) Stop() {
	r.mu.Lock()
	if !r.open {
		r.mu.Unlock()
		return
	}
	stop, done, fd := r.stop, r.finished, r.fd
	r.open = false
	r.mu.Unlock()

	stop()
	<-done

	_ = command(fd, cmdLEScanEnable, []byte{0x00, 0x00})
	_ = syscall.Close(fd)

	r.mu.Lock()
	r.fd = -1
	r.mu.Unlock()
	slog.Info("ble stopped")
}

// begin resets the controller and starts scanning. Held with mu.
func (r *Radio) begin(active bool) error {
	scan := make([]byte, 7)
	if active {
		scan[0] = 0x01
	}
	binary.LittleEndian.PutUint16(scan[1:], scanInterval)
	binary.LittleEndian.PutUint16(scan[3:], scanWindow)

	for _, step := range []struct {
		name   string
		opcode uint16
		params []byte
	}{
		{"reset", cmdReset, nil},
		{"scan parameters", cmdLEScanParams, scan},
		{"scan enable", cmdLEScanEnable, []byte{0x01, 0x00}},
	} {
		if err := command(r.fd, step.opcode, step.params); err != nil {
			return fmt.Errorf("ble: %s: %w", step.name, err)
		}
	}
	return nil
}

func (r *Radio) read(ctx context.Context, found func(Advertisement)) {
	defer close(r.finished)

	// The driver refuses a read larger than its own buffer, and an HCI event is at most 258 bytes.
	buf := make([]byte, 512)
	var held []byte

	for ctx.Err() == nil {
		n, err := syscall.Read(r.fd, buf)
		if err != nil {
			if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
				continue
			}
			if ctx.Err() == nil {
				slog.Error("ble read failed", "err", err)
			}
			return
		}
		if n == 0 {
			continue
		}

		held = r.parse(append(held, buf[:n]...), found)
	}
}

// parse takes whole packets off the front and returns the remainder. A read carries as many as the
// controller had ready, and the last can be cut short.
func (r *Radio) parse(b []byte, found func(Advertisement)) []byte {
	for len(b) >= 3 {
		if b[0] != h4Event {
			return nil
		}
		size := 3 + int(b[2])
		if len(b) < size {
			break
		}

		if b[1] == evtLEMeta {
			r.reports++
			reports(b[3:size], found)
		}
		b = b[size:]
	}

	return append([]byte(nil), b...)
}

// reports walks an LE Meta event: event type, address type, address, data length, data, RSSI.
func reports(p []byte, found func(Advertisement)) {
	if len(p) < 2 || p[0] != leAdvertisingReport {
		return
	}

	at := 2
	for range int(p[1]) {
		if at+9 > len(p) {
			return
		}
		length := int(p[at+8])
		end := at + 9 + length
		if end >= len(p) {
			return
		}

		a := Advertisement{AddressType: p[at+1], RSSI: int8(p[end])}
		copy(a.Address[:], p[at+2:at+8])
		a.Data = append([]byte(nil), p[at+9:end]...)
		found(a)

		at = end + 1
	}
}

// command writes one HCI command and waits for its Command Complete.
func command(fd int, opcode uint16, params []byte) error {
	pkt := make([]byte, 4, 4+len(params))
	pkt[0] = h4Command
	binary.LittleEndian.PutUint16(pkt[1:], opcode)
	pkt[3] = byte(len(params))
	pkt = append(pkt, params...)

	if _, err := syscall.Write(fd, pkt); err != nil {
		return err
	}

	buf := make([]byte, 512)
	for {
		n, err := syscall.Read(fd, buf)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return err
		}
		if n >= 7 && buf[0] == h4Event && buf[1] == evtCommandComplete {
			if binary.LittleEndian.Uint16(buf[4:]) != opcode {
				continue
			}
			if status := buf[6]; status != 0 {
				return fmt.Errorf("status 0x%02x", status)
			}
			return nil
		}
	}
}

func clearNonBlock(fd int) error {
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFL, 0)
	if errno != 0 {
		return fmt.Errorf("ble: reading descriptor flags: %w", errno)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFL, flags&^syscall.O_NONBLOCK); errno != 0 {
		return fmt.Errorf("ble: clearing O_NONBLOCK: %w", errno)
	}
	return nil
}
