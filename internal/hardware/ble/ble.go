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

	cmdReset               = 0x0C03
	cmdLEAdvertisingParams = 0x2006
	cmdLEAdvertisingData   = 0x2008
	cmdLEAdvertisingEnable = 0x200A
	cmdLEScanParams        = 0x200B
	cmdLEScanEnable        = 0x200C
)

// How much of the time the radio listens, in units of 0.625 ms: 18.75 ms out of every 200 ms. Wifi
// and Bluetooth share one antenna here, so a window equal to the interval never gives it back.
const (
	scanInterval        = 320
	scanWindow          = 30
	advertisingInterval = 1600
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
	scanning bool
	stop     context.CancelFunc
	// finished is both the reader's completion signal and its handoff of an unfinished H4 event.
	finished chan []byte
	// held carries an unfinished H4 event between synchronous startup commands.
	held []byte

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
	return r.scanning
}

// Running reports whether the controller is open for scanning or advertising.
func (r *Radio) Running() bool {
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

// Start opens the controller until Stop. Reports arrive on the reader's goroutine when scan is true.
// Active asks for scan responses, which transmits rather than only listening. Advertisement is raw
// advertising data; nil disables advertising.
func (r *Radio) Start(scan, active bool, advertisement []byte, found func(Advertisement)) error {
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

	if err := r.begin(scan, active, advertisement); err != nil {
		_ = syscall.Close(fd)
		r.fd = -1
		r.held = nil
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.stop, r.finished, r.open = cancel, make(chan []byte, 1), true
	r.scanning = scan
	held := r.held
	r.held = nil

	go r.read(ctx, found, held, r.finished)
	return nil
}

// Stop ends scanning and advertising and closes the node.
func (r *Radio) Stop() {
	r.mu.Lock()
	if !r.open {
		r.mu.Unlock()
		return
	}
	stop, done, fd := r.stop, r.finished, r.fd
	r.open = false
	r.scanning = false
	r.mu.Unlock()

	stop()
	held := <-done

	held, _ = command(fd, held, cmdLEScanEnable, []byte{0x00, 0x00})
	_, _ = command(fd, held, cmdLEAdvertisingEnable, []byte{0x00})
	_ = syscall.Close(fd)

	r.mu.Lock()
	r.fd = -1
	r.mu.Unlock()
	slog.Info("ble stopped")
}

// begin resets and configures the controller. Held with mu.
func (r *Radio) begin(scanning, active bool, advertisement []byte) error {
	if err := r.send("reset", cmdReset, nil); err != nil {
		return err
	}
	// This MTK controller can scan and advertise together, but only when scanning is enabled first.
	// Enabling advertising first leaves the subsequent scan command waiting indefinitely.
	if scanning {
		scan := make([]byte, 7)
		if active {
			scan[0] = 0x01
		}
		binary.LittleEndian.PutUint16(scan[1:], scanInterval)
		binary.LittleEndian.PutUint16(scan[3:], scanWindow)

		if err := r.send("scan parameters", cmdLEScanParams, scan); err != nil {
			return err
		}
		if err := r.send("scan enable", cmdLEScanEnable, []byte{0x01, 0x00}); err != nil {
			return err
		}
	}
	if len(advertisement) != 0 {
		return r.advertise(advertisement)
	}
	return nil
}

func (r *Radio) advertise(advertisement []byte) error {
	if len(advertisement) > 31 {
		return errors.New("ble: advertising data exceeds 31 bytes")
	}
	params := make([]byte, 15)
	binary.LittleEndian.PutUint16(params[0:], advertisingInterval)
	binary.LittleEndian.PutUint16(params[2:], advertisingInterval)
	params[4] = 0x03 // ADV_NONCONN_IND
	params[13] = 0x07

	data := make([]byte, 32)
	data[0] = byte(len(advertisement))
	copy(data[1:], advertisement)

	if err := r.send("advertising parameters", cmdLEAdvertisingParams, params); err != nil {
		return err
	}
	if err := r.send("advertising data", cmdLEAdvertisingData, data); err != nil {
		return err
	}
	return r.send("advertising enable", cmdLEAdvertisingEnable, []byte{0x01})
}

func (r *Radio) send(name string, opcode uint16, params []byte) error {
	var err error
	r.held, err = command(r.fd, r.held, opcode, params)
	if err != nil {
		return fmt.Errorf("ble: %s: %w", name, err)
	}
	return nil
}

func (r *Radio) read(ctx context.Context, found func(Advertisement), held []byte, finished chan<- []byte) {
	defer func() { finished <- held }()

	// The driver refuses a read larger than its own buffer, and an HCI event is at most 258 bytes.
	buf := make([]byte, 512)

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

		held, err = r.parse(append(held, buf[:n]...), found)
		if err != nil {
			slog.Error("ble event framing failed", "err", err)
			held = nil
			continue
		}
	}
}

// parse takes whole packets off the front and returns the remainder. A read carries as many as the
// controller had ready, and the last can be cut short.
func (r *Radio) parse(b []byte, found func(Advertisement)) ([]byte, error) {
	for {
		event, remainder, ok, err := nextEvent(b)
		if err != nil {
			return nil, err
		}
		if !ok {
			return append([]byte(nil), remainder...), nil
		}

		if event[1] == evtLEMeta {
			r.reports++
			reports(event[3:], found)
		}
		b = remainder
	}
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
func command(fd int, held []byte, opcode uint16, params []byte) ([]byte, error) {
	pkt := make([]byte, 4, 4+len(params))
	pkt[0] = h4Command
	binary.LittleEndian.PutUint16(pkt[1:], opcode)
	pkt[3] = byte(len(params))
	pkt = append(pkt, params...)

	if _, err := syscall.Write(fd, pkt); err != nil {
		return held, err
	}

	buf := make([]byte, 512)
	for {
		n, err := syscall.Read(fd, buf)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return held, err
		}
		if n == 0 {
			continue
		}

		held = append(held, buf[:n]...)
		var complete bool
		held, complete, err = commandResult(held, opcode)
		if err != nil {
			return held, err
		}
		if complete {
			return held, nil
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
