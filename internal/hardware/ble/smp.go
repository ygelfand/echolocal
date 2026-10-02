package ble

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// SMP commands.
const (
	smpPairingRequest   = 0x01
	smpPairingResponse  = 0x02
	smpPairingConfirm   = 0x03
	smpPairingRandom    = 0x04
	smpPairingFailed    = 0x05
	smpEncryptionInfo   = 0x06
	smpCentralIdent     = 0x07
	smpIdentityInfo     = 0x08
	smpIdentityAddress  = 0x09
	smpSecurityRequest  = 0x0B
	smpPairingPublicKey = 0x0C
	smpPairingDHKeyChk  = 0x0D
)

// What this side asks for: no display and no keyboard, so Just Works; bonding, so the device keeps
// the keys and takes an encrypted link without pairing again; Secure Connections where the device
// has it.
const (
	ioNoInputNoOutput = 0x03
	authBonding       = 0x01
	authSC            = 0x08
	distEncKey        = 0x01
	distIDKey         = 0x02
	maxKeySize        = 16
	minKeySize        = 7
	smpTimeout        = 30 * time.Second
)

// Pairing Failed reasons this side sends.
const (
	failConfirmValue   = 0x04
	failNotSupported   = 0x05
	failEncKeySize     = 0x06
	failUnspecified    = 0x08
	failInvalidParams  = 0x0A
	failDHKeyCheck     = 0x0B
	failCmdUnsupported = 0x07
)

// PairingError is pairing the device refused or that failed a check.
type PairingError struct {
	Reason uint8
	// Remote is whether the device sent it.
	Remote bool
}

func (e *PairingError) Error() string {
	side := "local"
	if e.Remote {
		side = "remote"
	}
	return fmt.Sprintf("ble: pairing failed (%s, reason 0x%02x)", side, e.Reason)
}

// Keys is what pairing leaves: the key that encrypts the link again next time, and the device's
// identity where it gave one.
type Keys struct {
	LTK  [16]byte
	EDIV uint16
	Rand [8]byte
	// Size is the key's size in bytes, which may be less than 16.
	Size int
	// Secure is LE Secure Connections, as opposed to legacy pairing.
	Secure bool

	IRK          [16]byte
	Identity     [6]byte
	IdentityType uint8
	HasIdentity  bool
}

// security is one link's SMP state.
type security struct {
	mu sync.Mutex
	// in carries SMP commands to the pairing in progress, if any.
	in chan []byte
	// encrypted carries Encryption Change and Key Refresh outcomes.
	encrypted chan uint8
	onRequest func()

	pairing sync.Mutex
}

// OnSecurityRequest sets what happens when the device asks for security, which a device does when a
// client reaches for something it only serves over an encrypted link.
func (c *Conn) OnSecurityRequest(f func()) {
	c.sec.mu.Lock()
	c.sec.onRequest = f
	c.sec.mu.Unlock()
}

// smp takes one SMP command off the link, on the reader.
func (c *Conn) smp(p []byte) {
	if len(p) == 0 {
		return
	}
	c.sec.mu.Lock()
	in, onRequest := c.sec.in, c.sec.onRequest
	c.sec.mu.Unlock()

	switch {
	case in != nil:
		select {
		case in <- append([]byte(nil), p...):
		default:
		}
	case p[0] == smpSecurityRequest:
		if onRequest != nil {
			go onRequest()
		}
	case p[0] == smpPairingRequest:
		// This side is central, and only the central starts pairing.
		c.reply(cidSMP, []byte{smpPairingFailed, failCmdUnsupported})
	}
}

// encryption hands an Encryption Change or Key Refresh Complete to the link.
func (l *links) encryption(handle uint16, status uint8) {
	if c := l.get(handle); c != nil {
		select {
		case c.sec.encrypted <- status:
		default:
		}
	}
}

// Encrypt encrypts the link with keys from an earlier pairing.
func (c *Conn) Encrypt(ctx context.Context, k Keys) error {
	return c.encrypt(ctx, k.LTK, k.EDIV, k.Rand)
}

func (c *Conn) encrypt(ctx context.Context, ltk [16]byte, ediv uint16, rnd [8]byte) error {
	// Drain an outcome left over from anything earlier.
	select {
	case <-c.sec.encrypted:
	default:
	}
	params := make([]byte, 28)
	binary.LittleEndian.PutUint16(params, c.Handle)
	copy(params[2:], rnd[:])
	binary.LittleEndian.PutUint16(params[10:], ediv)
	copy(params[12:], ltk[:])
	if _, err := c.r.Command(cmdLEEnableEncryption, params); err != nil {
		return fmt.Errorf("ble: starting encryption: %w", err)
	}
	select {
	case status := <-c.sec.encrypted:
		if status != 0 {
			return StatusError(status)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.Done():
		return ErrClosed
	}
}

// Pair pairs with the device as initiator, Just Works, encrypts the link, and returns the keys the
// device distributed for next time. The device has to be willing: many only are in a pairing mode.
func (c *Conn) Pair(ctx context.Context) (Keys, error) {
	c.sec.pairing.Lock()
	defer c.sec.pairing.Unlock()

	ctx, cancel := context.WithTimeout(ctx, smpTimeout)
	defer cancel()

	in := make(chan []byte, 16)
	c.sec.mu.Lock()
	c.sec.in = in
	c.sec.mu.Unlock()
	defer func() {
		c.sec.mu.Lock()
		c.sec.in = nil
		c.sec.mu.Unlock()
	}()

	p := &pairing{c: c, ctx: ctx, in: in}
	k, err := p.run()
	if err != nil {
		var pe *PairingError
		if !errors.As(err, &pe) || !pe.Remote {
			reason := uint8(failUnspecified)
			if pe != nil {
				reason = pe.Reason
			}
			_ = c.send(context.Background(), cidSMP, []byte{smpPairingFailed, reason})
		}
		return Keys{}, err
	}
	slog.Info("ble paired", "address", fmt.Sprintf("%012X", c.Addr()), "secure", k.Secure, "key_size", k.Size)
	return k, nil
}

type pairing struct {
	c    *Conn
	ctx  context.Context
	in   chan []byte
	preq [7]byte
	pres [7]byte
}

func (p *pairing) send(pdu ...byte) error { return p.c.send(p.ctx, cidSMP, pdu) }

// expect waits for one command, and turns Pairing Failed into its reason.
func (p *pairing) expect(code uint8, size int) ([]byte, error) {
	select {
	case pdu := <-p.in:
		if pdu[0] == smpPairingFailed && len(pdu) >= 2 {
			return nil, &PairingError{Reason: pdu[1], Remote: true}
		}
		if pdu[0] != code || len(pdu) != 1+size {
			return nil, &PairingError{Reason: failInvalidParams}
		}
		return pdu[1:], nil
	case <-p.ctx.Done():
		return nil, fmt.Errorf("ble: pairing: %w", p.ctx.Err())
	case <-p.c.Done():
		return nil, ErrClosed
	}
}

// ours is this side's address followed by its type, as f5 and f6 take it; theirs the device's.
func (p *pairing) ours() [7]byte {
	var a [7]byte
	copy(a[:], p.c.r.address[:])
	a[6] = 0 // the controller's public address
	return a
}

func (p *pairing) theirs() [7]byte {
	var a [7]byte
	for i := range 6 {
		a[i] = p.c.Address[5-i]
	}
	a[6] = p.c.AddressType
	return a
}

func (p *pairing) run() (Keys, error) {
	p.preq = [7]byte{smpPairingRequest, ioNoInputNoOutput, 0x00, authBonding | authSC, maxKeySize, 0x00, distEncKey | distIDKey}
	if err := p.send(p.preq[:]...); err != nil {
		return Keys{}, err
	}
	rsp, err := p.expect(smpPairingResponse, 6)
	if err != nil {
		return Keys{}, err
	}
	p.pres[0] = smpPairingResponse
	copy(p.pres[1:], rsp)

	var k Keys
	k.Size = min(maxKeySize, int(p.pres[4]))
	if k.Size < minKeySize {
		return Keys{}, &PairingError{Reason: failEncKeySize}
	}
	k.Secure = p.pres[3]&authSC != 0

	var key [16]byte
	if k.Secure {
		key, err = p.secureConnections()
	} else {
		key, err = p.legacy()
	}
	if err != nil {
		return Keys{}, err
	}
	for i := k.Size; i < 16; i++ {
		key[i] = 0
	}
	if err := p.c.encrypt(p.ctx, key, 0, [8]byte{}); err != nil {
		return Keys{}, err
	}
	if k.Secure {
		k.LTK = key
	}
	return k, p.distribution(&k)
}

// legacy is LE legacy pairing, Just Works: the temporary key is zero, and the confirm exchange only
// proves both sides computed the same short term key.
func (p *pairing) legacy() ([16]byte, error) {
	var tk, mrand [16]byte
	if _, err := rand.Read(mrand[:]); err != nil {
		return tk, err
	}
	theirs, ours := p.theirs(), p.ours()
	ra, ia := [6]byte(theirs[:6]), [6]byte(ours[:6])
	confirm := func(r [16]byte) [16]byte {
		return c1(tk, r, p.preq, p.pres, 0, ia, p.c.AddressType, ra)
	}

	mconfirm := confirm(mrand)
	if err := p.send(append([]byte{smpPairingConfirm}, mconfirm[:]...)...); err != nil {
		return tk, err
	}
	sconfirm, err := p.expect(smpPairingConfirm, 16)
	if err != nil {
		return tk, err
	}
	if err := p.send(append([]byte{smpPairingRandom}, mrand[:]...)...); err != nil {
		return tk, err
	}
	srandBytes, err := p.expect(smpPairingRandom, 16)
	if err != nil {
		return tk, err
	}
	srand := [16]byte(srandBytes)
	if confirm(srand) != [16]byte(sconfirm) {
		return tk, &PairingError{Reason: failConfirmValue}
	}
	return s1(tk, srand, mrand), nil
}

// secureConnections is LE Secure Connections, Just Works: P-256 Diffie-Hellman, the device's
// commitment to its nonce, and a check that both sides derived the same keys.
func (p *pairing) secureConnections() ([16]byte, error) {
	var ltk [16]byte
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return ltk, err
	}
	// Uncompressed: 0x04, then X and Y most significant first. SMP carries each least significant first.
	raw := priv.PublicKey().Bytes()
	var pka [64]byte
	copy(pka[:32], reversed(raw[1:33]))
	copy(pka[32:], reversed(raw[33:65]))
	if err := p.send(append([]byte{smpPairingPublicKey}, pka[:]...)...); err != nil {
		return ltk, err
	}

	pkbBytes, err := p.expect(smpPairingPublicKey, 64)
	if err != nil {
		return ltk, err
	}
	pkb := [64]byte(pkbBytes)
	if pkb == pka {
		return ltk, &PairingError{Reason: failInvalidParams} // a reflected key
	}
	uncompressed := append([]byte{0x04}, reversed(pkb[:32])...)
	uncompressed = append(uncompressed, reversed(pkb[32:])...)
	peer, err := ecdh.P256().NewPublicKey(uncompressed)
	if err != nil {
		return ltk, &PairingError{Reason: failInvalidParams}
	}
	secret, err := priv.ECDH(peer)
	if err != nil {
		return ltk, &PairingError{Reason: failDHKeyCheck}
	}
	dhkey := [32]byte(reversed(secret))

	cb, err := p.expect(smpPairingConfirm, 16)
	if err != nil {
		return ltk, err
	}
	var na [16]byte
	if _, err := rand.Read(na[:]); err != nil {
		return ltk, err
	}
	if err := p.send(append([]byte{smpPairingRandom}, na[:]...)...); err != nil {
		return ltk, err
	}
	nbBytes, err := p.expect(smpPairingRandom, 16)
	if err != nil {
		return ltk, err
	}
	nb := [16]byte(nbBytes)
	if f4([32]byte(pkb[:32]), [32]byte(pka[:32]), nb, 0) != [16]byte(cb) {
		return ltk, &PairingError{Reason: failConfirmValue}
	}

	a1, a2 := p.ours(), p.theirs()
	macKey, ltk := f5(dhkey, na, nb, a1, a2)
	var zero [16]byte
	ea := f6(macKey, na, nb, zero, [3]byte(p.preq[1:4]), a1, a2)
	if err := p.send(append([]byte{smpPairingDHKeyChk}, ea[:]...)...); err != nil {
		return ltk, err
	}
	eb, err := p.expect(smpPairingDHKeyChk, 16)
	if err != nil {
		return ltk, err
	}
	if f6(macKey, nb, na, zero, [3]byte(p.pres[1:4]), a2, a1) != [16]byte(eb) {
		return ltk, &PairingError{Reason: failDHKeyCheck}
	}
	return ltk, nil
}

// distribution collects the keys the device agreed to send once the link is encrypted. Under
// Secure Connections the LTK is derived, not sent.
func (p *pairing) distribution(k *Keys) error {
	want := p.pres[6] & p.preq[6]
	if k.Secure {
		want &^= distEncKey
	}
	var gotEnc, gotMaster, gotIRK, gotAddr bool
	for {
		done := (want&distEncKey == 0 || gotEnc && gotMaster) && (want&distIDKey == 0 || gotIRK && gotAddr)
		if done {
			return nil
		}
		select {
		case pdu := <-p.in:
			switch {
			case pdu[0] == smpPairingFailed && len(pdu) >= 2:
				return &PairingError{Reason: pdu[1], Remote: true}
			case pdu[0] == smpEncryptionInfo && len(pdu) == 17:
				copy(k.LTK[:], pdu[1:])
				gotEnc = true
			case pdu[0] == smpCentralIdent && len(pdu) == 11:
				k.EDIV = binary.LittleEndian.Uint16(pdu[1:])
				copy(k.Rand[:], pdu[3:])
				gotMaster = true
			case pdu[0] == smpIdentityInfo && len(pdu) == 17:
				copy(k.IRK[:], pdu[1:])
				gotIRK = true
			case pdu[0] == smpIdentityAddress && len(pdu) == 8:
				k.IdentityType = pdu[1]
				for i := range 6 {
					k.Identity[i] = pdu[7-i]
				}
				k.HasIdentity = true
				gotAddr = true
			}
		case <-p.ctx.Done():
			return fmt.Errorf("ble: waiting for keys: %w", p.ctx.Err())
		case <-p.c.Done():
			return ErrClosed
		}
	}
}
