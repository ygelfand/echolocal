package ble

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"testing"
	"time"
)

// responder is the device's half of Just Works pairing, built on the same primitives the tests
// above check against the specification's sample data.
type responder struct {
	t      *testing.T
	p      *peripheral
	secure bool

	preq, pres [7]byte
	central    [7]byte // address and type of the initiator
	device     [7]byte

	priv     *ecdh.PrivateKey
	pka, pkb [64]byte
	dhkey    [32]byte
	na, nb   [16]byte
	mconfirm [16]byte
	key      [16]byte // what the link is encrypted with

	// Distributed for next time.
	ltk  [16]byte
	ediv uint16
	rnd  [8]byte
	irk  [16]byte
}

func newResponder(t *testing.T, p *peripheral, secure bool) *responder {
	rs := &responder{t: t, p: p, secure: secure, ediv: 0x1234, rnd: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}}
	_, _ = rand.Read(rs.ltk[:])
	_, _ = rand.Read(rs.irk[:])
	copy(rs.central[:], p.r.address[:])
	rs.device = [7]byte{0xe2, 0xf8, 0x16, 0xdd, 0x5e, 0x68, 1}
	p.smp = rs.receive
	p.encrypt = rs.encrypt
	return rs
}

func (rs *responder) receive(pdu []byte) {
	switch pdu[0] {
	case smpPairingRequest:
		copy(rs.preq[:], pdu)
		auth := byte(authBonding)
		if rs.secure {
			auth |= authSC
		}
		rs.pres = [7]byte{smpPairingResponse, ioNoInputNoOutput, 0, auth, 16, 0, distEncKey | distIDKey}
		rs.p.sendOn(cidSMP, rs.pres[:])

	case smpPairingPublicKey:
		copy(rs.pka[:], pdu[1:])
		rs.priv, _ = ecdh.P256().GenerateKey(rand.Reader)
		raw := rs.priv.PublicKey().Bytes()
		copy(rs.pkb[:32], reversed(raw[1:33]))
		copy(rs.pkb[32:], reversed(raw[33:]))
		peer, err := ecdh.P256().NewPublicKey(append(append([]byte{4}, reversed(rs.pka[:32])...), reversed(rs.pka[32:])...))
		if err != nil {
			rs.t.Errorf("central public key: %v", err)
			return
		}
		secret, _ := rs.priv.ECDH(peer)
		rs.dhkey = [32]byte(reversed(secret))
		_, _ = rand.Read(rs.nb[:])
		rs.p.sendOn(cidSMP, append([]byte{smpPairingPublicKey}, rs.pkb[:]...))
		cb := f4([32]byte(rs.pkb[:32]), [32]byte(rs.pka[:32]), rs.nb, 0)
		rs.p.sendOn(cidSMP, append([]byte{smpPairingConfirm}, cb[:]...))

	case smpPairingConfirm: // legacy
		copy(rs.mconfirm[:], pdu[1:])
		_, _ = rand.Read(rs.nb[:])
		sconfirm := rs.c1(rs.nb)
		rs.p.sendOn(cidSMP, append([]byte{smpPairingConfirm}, sconfirm[:]...))

	case smpPairingRandom:
		copy(rs.na[:], pdu[1:])
		if !rs.secure {
			if rs.c1(rs.na) != rs.mconfirm {
				rs.p.sendOn(cidSMP, []byte{smpPairingFailed, failConfirmValue})
				return
			}
			rs.key = s1([16]byte{}, rs.nb, rs.na)
		}
		rs.p.sendOn(cidSMP, append([]byte{smpPairingRandom}, rs.nb[:]...))

	case smpPairingDHKeyChk:
		macKey, ltk := f5(rs.dhkey, rs.na, rs.nb, rs.central, rs.device)
		var zero [16]byte
		if f6(macKey, rs.na, rs.nb, zero, [3]byte(rs.preq[1:4]), rs.central, rs.device) != [16]byte(pdu[1:]) {
			rs.p.sendOn(cidSMP, []byte{smpPairingFailed, failDHKeyCheck})
			return
		}
		rs.key, rs.ltk = ltk, ltk
		eb := f6(macKey, rs.nb, rs.na, zero, [3]byte(rs.pres[1:4]), rs.device, rs.central)
		rs.p.sendOn(cidSMP, append([]byte{smpPairingDHKeyChk}, eb[:]...))
	}
}

func (rs *responder) c1(r [16]byte) [16]byte {
	return c1([16]byte{}, r, rs.preq, rs.pres, 0, [6]byte(rs.central[:6]), rs.device[6], [6]byte(rs.device[:6]))
}

// encrypt accepts the pairing key once, and after that the key it distributed.
func (rs *responder) encrypt(params []byte) byte {
	ltk := [16]byte(params[12:28])
	ediv := binary.LittleEndian.Uint16(params[10:])
	if ltk == rs.key && ediv == 0 {
		// Key distribution follows the first encryption.
		rs.p.afterEncrypt = func() {
			if !rs.secure {
				rs.p.sendOn(cidSMP, append([]byte{smpEncryptionInfo}, rs.ltk[:]...))
				rs.p.sendOn(cidSMP, append(append([]byte{smpCentralIdent}, u16(rs.ediv)...), rs.rnd[:]...))
			}
			rs.p.sendOn(cidSMP, append([]byte{smpIdentityInfo}, rs.irk[:]...))
			rs.p.sendOn(cidSMP, append([]byte{smpIdentityAddress, 0x00}, rs.device[:6]...))
		}
		return 0
	}
	if ltk == rs.ltk && ediv == rs.ediv*boolInt(!rs.secure) && [8]byte(params[2:10]) == rs.rnd || rs.secure && ltk == rs.ltk {
		return 0
	}
	return 0x06
}

func boolInt(b bool) uint16 {
	if b {
		return 1
	}
	return 0
}

func TestPairing(t *testing.T) {
	for _, secure := range []bool{true, false} {
		name := "legacy"
		if secure {
			name = "secure connections"
		}
		t.Run(name, func(t *testing.T) {
			p := newPeripheral(t)
			p.r.address = [6]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
			rs := newResponder(t, p, secure)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			c, err := p.r.Connect(ctx, [6]byte{0x68, 0x5e, 0xdd, 0x16, 0xf8, 0xe2}, 1)
			if err != nil {
				t.Fatal(err)
			}
			k, err := c.Pair(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if k.Secure != secure || k.Size != 16 || k.LTK != rs.ltk || !k.HasIdentity || k.IRK != rs.irk {
				t.Errorf("keys = %+v", k)
			}
			if !secure && (k.EDIV != rs.ediv || k.Rand != rs.rnd) {
				t.Errorf("central identification = %x %x", k.EDIV, k.Rand)
			}
			if k.Identity != [6]byte{0x68, 0x5e, 0xdd, 0x16, 0xf8, 0xe2} {
				t.Errorf("identity = %x", k.Identity)
			}

			// Next time: the stored keys encrypt the link without pairing.
			if err := c.Encrypt(ctx, k); err != nil {
				t.Errorf("encrypting with the bond: %v", err)
			}
			if err := c.Encrypt(ctx, Keys{}); err == nil {
				t.Error("encrypting with a key the device does not have succeeded")
			}
		})
	}
}

func TestPairingRefused(t *testing.T) {
	p := newPeripheral(t)
	p.smp = func(pdu []byte) {
		if pdu[0] == smpPairingRequest {
			p.sendOn(cidSMP, []byte{smpPairingFailed, 0x05})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := p.r.Connect(ctx, [6]byte{1, 2, 3, 4, 5, 6}, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Pair(ctx)
	pe, ok := err.(*PairingError)
	if !ok || !pe.Remote || pe.Reason != 0x05 {
		t.Errorf("err = %v", err)
	}
}
