// Package ble scans for Bluetooth Low Energy advertisements over the controller's HCI node, and
// connects to devices as a GATT client.
package ble

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"syscall"
	"time"
)

// Node is the controller's HCI character device.
const Node = "/dev/stpbt"

const (
	h4Command = 0x01

	cmdDisconnect          = 0x0406
	cmdReset               = 0x0C03
	cmdReadBufferSize      = 0x1005
	cmdReadBDAddr          = 0x1009
	cmdLEReadBufferSize    = 0x2002
	cmdLEAdvertisingParams = 0x2006
	cmdLEAdvertisingData   = 0x2008
	cmdLEAdvertisingEnable = 0x200A
	cmdLEScanParams        = 0x200B
	cmdLEScanEnable        = 0x200C
	cmdLECreateConnection  = 0x200D
	cmdLECreateCancel      = 0x200E
	cmdLEConnectionUpdate  = 0x2013
	cmdLEEnableEncryption  = 0x2019
	cmdLELTKNegativeReply  = 0x201B
	cmdLERemoteParamReply  = 0x2020
)

// How much of the time the radio listens, in units of 0.625 ms: 18.75 ms out of every 200 ms. Wifi
// and Bluetooth share one antenna here, so a window equal to the interval never gives it back.
const (
	scanInterval        = 320
	scanWindow          = 30
	advertisingInterval = 1600
)

// commandTimeout bounds a command sent while the reader runs. The controller answers in
// milliseconds; waiting longer means it has stopped answering.
const commandTimeout = 5 * time.Second

// ErrNotRunning is a command or connection asked of a closed controller.
var ErrNotRunning = errors.New("ble: controller is not running")

// Advertisement is one LE advertising report.
type Advertisement struct {
	// EventType is the PDU the report came from: 0 connectable, 1 directed, 2 scannable, 3 not
	// connectable, 4 a scan response.
	EventType   uint8
	Address     [6]byte
	AddressType uint8
	RSSI        int8
	Data        []byte
}

// Addr is the address as a big-endian integer.
func (a Advertisement) Addr() uint64 { return addrUint(a.Address) }

func addrUint(a [6]byte) uint64 {
	var v uint64
	for _, b := range a {
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
	active   bool
	stop     context.CancelFunc
	// finished is both the reader's completion signal and its handoff of an unfinished H4 event.
	finished chan []byte
	// held carries an unfinished H4 event between synchronous startup commands.
	held  []byte
	found func(Advertisement)

	reports uint64

	// wmu keeps packets whole on the node: commands and ACL data are written from several goroutines.
	wmu sync.Mutex

	// cmd is one command at a time once the reader owns the node, and pending the one in flight.
	cmd     sync.Mutex
	pmu     sync.Mutex
	pending *pendingCommand

	acl  aclBuffers
	link links

	// address is the controller's public address, least significant octet first, which pairing
	// mixes into its keys.
	address [6]byte

	// sink replaces the node for writes in tests, where there is no controller.
	sink func([]byte) error
}

type pendingCommand struct {
	opcode uint16
	done   chan commandReply
}

type commandReply struct {
	params []byte
	err    error
}

var (
	once  sync.Once
	radio *Radio
)

// Get is the radio.
func Get() *Radio {
	once.Do(func() {
		radio = &Radio{fd: -1}
		radio.acl.init()
		radio.link.init()
	})
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
	r.scanning, r.active, r.found = scan, active, found
	held := r.held
	r.held = nil

	go r.read(ctx, held, r.finished)
	return nil
}

// Stop ends scanning, advertising and every connection, and closes the node.
func (r *Radio) Stop() {
	if !r.Running() {
		return
	}
	// Disconnecting needs the reader, to hear the controller confirm it.
	r.link.closeAll(r)

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
	// The reader is blocked in read until the controller says something, which with nothing
	// scanning may be never. A command it answers at once is something to say.
	_ = r.write(readLocalVersion)
	held := <-done
	r.failPending(ErrNotRunning)
	r.acl.reset(0, 0)

	held, _, _ = command(fd, held, cmdLEScanEnable, []byte{0x00, 0x00})
	_, _, _ = command(fd, held, cmdLEAdvertisingEnable, []byte{0x00})
	_ = syscall.Close(fd)

	r.mu.Lock()
	r.fd = -1
	r.mu.Unlock()
	slog.Info("ble stopped")
}

// readLocalVersion is a command every controller answers straight away.
var readLocalVersion = []byte{h4Command, 0x01, 0x10, 0x00}

// Rescan changes between active and passive scanning without closing the controller, so that the
// connections it holds survive Home Assistant changing its mind about scan responses.
func (r *Radio) Rescan(active bool) error {
	r.mu.Lock()
	scanning, was := r.scanning, r.active
	r.mu.Unlock()
	if !scanning || active == was {
		return nil
	}

	if _, err := r.Command(cmdLEScanEnable, []byte{0x00, 0x00}); err != nil {
		return err
	}
	if _, err := r.Command(cmdLEScanParams, scanParams(active)); err != nil {
		return err
	}
	if _, err := r.Command(cmdLEScanEnable, []byte{0x01, 0x00}); err != nil {
		return err
	}
	r.mu.Lock()
	r.active = active
	r.mu.Unlock()
	return nil
}

// Active reports whether the running scan asks for scan responses.
func (r *Radio) Active() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

func scanParams(active bool) []byte {
	scan := make([]byte, 7)
	if active {
		scan[0] = 0x01
	}
	binary.LittleEndian.PutUint16(scan[1:], scanInterval)
	binary.LittleEndian.PutUint16(scan[3:], scanWindow)
	return scan
}

// begin resets and configures the controller. Held with mu.
func (r *Radio) begin(scanning, active bool, advertisement []byte) error {
	if err := r.send("reset", cmdReset, nil); err != nil {
		return err
	}
	if err := r.bufferSize(); err != nil {
		return err
	}
	var params []byte
	var err error
	if r.held, params, err = command(r.fd, r.held, cmdReadBDAddr, nil); err != nil {
		return fmt.Errorf("ble: reading the controller's address: %w", err)
	}
	if len(params) >= 6 {
		copy(r.address[:], params[:6])
	}
	// This MTK controller can scan and advertise together, but only when scanning is enabled first.
	// Enabling advertising first leaves the subsequent scan command waiting indefinitely.
	if scanning {
		if err := r.send("scan parameters", cmdLEScanParams, scanParams(active)); err != nil {
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

// bufferSize learns how much ACL data the controller takes at once. LE has its own buffers unless
// the controller reports none, in which case LE shares the BR/EDR ones. Held with mu.
func (r *Radio) bufferSize() error {
	var err error
	var params []byte
	r.held, params, err = command(r.fd, r.held, cmdLEReadBufferSize, nil)
	if err != nil {
		return fmt.Errorf("ble: LE buffer size: %w", err)
	}
	size, count := 0, 0
	if len(params) >= 3 {
		size, count = int(binary.LittleEndian.Uint16(params)), int(params[2])
	}
	if size == 0 || count == 0 {
		r.held, params, err = command(r.fd, r.held, cmdReadBufferSize, nil)
		if err != nil {
			return fmt.Errorf("ble: buffer size: %w", err)
		}
		if len(params) >= 5 {
			size, count = int(binary.LittleEndian.Uint16(params)), int(binary.LittleEndian.Uint16(params[3:]))
		}
	}
	if size == 0 || count == 0 {
		return errors.New("ble: controller reports no ACL buffers")
	}
	r.acl.reset(size, count)
	slog.Debug("ble acl buffers", "size", size, "count", count)
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
	r.held, _, err = command(r.fd, r.held, opcode, params)
	if err != nil {
		return fmt.Errorf("ble: %s: %w", name, err)
	}
	return nil
}

// Command sends one HCI command through the running reader and waits for the controller to finish
// it: Command Complete returns its parameters after the status, and a Command Status that accepts
// the command returns none. What such a command goes on to do arrives later as its own event.
//
// Never call it from the reader's goroutine, which is the only one that can hear the answer.
func (r *Radio) Command(opcode uint16, params []byte) ([]byte, error) {
	r.cmd.Lock()
	defer r.cmd.Unlock()

	if !r.Running() {
		return nil, ErrNotRunning
	}

	p := &pendingCommand{opcode: opcode, done: make(chan commandReply, 1)}
	r.pmu.Lock()
	r.pending = p
	r.pmu.Unlock()
	defer func() {
		r.pmu.Lock()
		if r.pending == p {
			r.pending = nil
		}
		r.pmu.Unlock()
	}()

	pkt := make([]byte, 4, 4+len(params))
	pkt[0] = h4Command
	binary.LittleEndian.PutUint16(pkt[1:], opcode)
	pkt[3] = byte(len(params))
	if err := r.write(append(pkt, params...)); err != nil {
		return nil, err
	}

	select {
	case reply := <-p.done:
		return reply.params, reply.err
	case <-time.After(commandTimeout):
		return nil, fmt.Errorf("ble: command 0x%04x: no answer from the controller", opcode)
	}
}

// commandEvent hands a Command Complete or Command Status to the command waiting for it.
func (r *Radio) commandEvent(event []byte) {
	r.pmu.Lock()
	p := r.pending
	r.pmu.Unlock()
	if p == nil {
		return
	}

	done, params, err := completes(event, p.opcode)
	if !done && err == nil && event[1] == evtCommandStatus && len(event) >= 7 &&
		binary.LittleEndian.Uint16(event[5:]) == p.opcode {
		done = true // accepted; the outcome follows as another event
	}
	if !done && err == nil {
		return
	}

	r.pmu.Lock()
	if r.pending == p {
		r.pending = nil
		p.done <- commandReply{params: params, err: err}
	}
	r.pmu.Unlock()
}

func (r *Radio) failPending(err error) {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	if r.pending != nil {
		r.pending.done <- commandReply{err: err}
		r.pending = nil
	}
}

// write puts one whole H4 packet on the node.
func (r *Radio) write(pkt []byte) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if r.sink != nil {
		return r.sink(pkt)
	}

	r.mu.Lock()
	fd := r.fd
	r.mu.Unlock()
	if fd < 0 {
		return ErrNotRunning
	}
	_, err := syscall.Write(fd, pkt)
	return err
}

func (r *Radio) read(ctx context.Context, held []byte, finished chan<- []byte) {
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

		held, err = r.parse(append(held, buf[:n]...))
		if err != nil {
			slog.Error("ble event framing failed", "err", err)
			held = nil
			continue
		}
	}
}

// parse takes whole packets off the front and returns the remainder. A read carries as many as the
// controller had ready, and the last can be cut short.
func (r *Radio) parse(b []byte) ([]byte, error) {
	for {
		pkt, remainder, ok, err := nextEvent(b)
		if err != nil {
			return nil, err
		}
		if !ok {
			return append([]byte(nil), remainder...), nil
		}
		r.dispatch(pkt)
		b = remainder
	}
}

// dispatch routes one packet. It runs on the reader and must not block: nothing else can hear the
// controller while it waits.
func (r *Radio) dispatch(pkt []byte) {
	if pkt[0] == h4ACL {
		r.link.data(r, pkt)
		return
	}

	switch pkt[1] {
	case evtCommandComplete, evtCommandStatus:
		r.commandEvent(pkt)
	case evtNumCompletedPackets:
		r.acl.completed(pkt[3:])
	case evtEncryptionChange, evtKeyRefreshComplete:
		if len(pkt) >= 6 {
			status := pkt[3]
			if pkt[1] == evtEncryptionChange && status == 0 && len(pkt) >= 7 && pkt[6] == 0 {
				status = statusConnectionFailed // the link came out unencrypted
			}
			r.link.encryption(binary.LittleEndian.Uint16(pkt[4:])&0x0fff, status)
		}
	case evtDisconnectionComplete:
		if len(pkt) >= 7 {
			handle := binary.LittleEndian.Uint16(pkt[4:]) & 0x0fff
			r.acl.forget(handle)
			r.link.disconnected(handle, pkt[6])
		}
	case evtLEMeta:
		if len(pkt) < 4 {
			return
		}
		switch pkt[3] {
		case leAdvertisingReport:
			r.mu.Lock()
			r.reports++
			found := r.found
			r.mu.Unlock()
			if found != nil {
				reports(pkt[3:], found)
			}
		case leConnectionComplete, leEnhancedConnectionComplete:
			r.link.connected(pkt[3:])
		case leLongTermKeyRequest:
			// No keys are kept, so no link is encrypted with one.
			if len(pkt) >= 6 {
				handle := append([]byte(nil), pkt[4:6]...)
				go func() { _, _ = r.Command(cmdLELTKNegativeReply, handle) }()
			}
		case leRemoteConnParamRequest:
			// Accept what the peripheral asks for: it knows what it needs to stay up.
			if len(pkt) >= 14 {
				reply := make([]byte, 14)
				copy(reply, pkt[4:14])
				go func() { _, _ = r.Command(cmdLERemoteParamReply, reply) }()
			}
		}
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

		a := Advertisement{EventType: p[at], AddressType: p[at+1], RSSI: int8(p[end])}
		// HCI carries an address least significant octet first. Address is the MAC as it is written,
		// which is the order a resolvable private address has to be in to be matched against an IRK.
		for i := range a.Address {
			a.Address[i] = p[at+7-i]
		}
		a.Data = append([]byte(nil), p[at+9:end]...)
		found(a)

		at = end + 1
	}
}

// command writes one HCI command and waits for its Command Complete, before the reader runs.
func command(fd int, held []byte, opcode uint16, params []byte) ([]byte, []byte, error) {
	pkt := make([]byte, 4, 4+len(params))
	pkt[0] = h4Command
	binary.LittleEndian.PutUint16(pkt[1:], opcode)
	pkt[3] = byte(len(params))
	pkt = append(pkt, params...)

	if _, err := syscall.Write(fd, pkt); err != nil {
		return held, nil, err
	}

	buf := make([]byte, 512)
	for {
		n, err := syscall.Read(fd, buf)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return held, nil, err
		}
		if n == 0 {
			continue
		}

		held = append(held, buf[:n]...)
		var complete bool
		var result []byte
		held, result, complete, err = commandResult(held, opcode)
		if err != nil {
			return held, nil, err
		}
		if complete {
			return held, result, nil
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
