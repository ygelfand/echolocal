package ble

import (
	"context"
	"encoding/binary"
	"sync"
)

// ACL packet boundary flags, in the top of the handle field.
const (
	pbFirstNonFlushable = 0x0000 // host to controller, start of an L2CAP frame
	pbContinuation      = 0x1000
	pbFirstFlushable    = 0x2000 // controller to host, start of an L2CAP frame
)

// aclBuffers is the controller's flow control: it takes a fixed number of ACL packets of a fixed
// size, and gives each buffer back with Number Of Completed Packets.
type aclBuffers struct {
	mu          sync.Mutex
	cond        *sync.Cond
	size        int
	free        int
	outstanding map[uint16]int
}

func (a *aclBuffers) init() {
	a.cond = sync.NewCond(&a.mu)
	a.outstanding = map[uint16]int{}
}

func (a *aclBuffers) reset(size, count int) {
	a.mu.Lock()
	a.size, a.free = size, count
	clear(a.outstanding)
	a.mu.Unlock()
	a.cond.Broadcast()
}

// completed reads Number Of Completed Packets: a count of handles, then handle and count pairs.
func (a *aclBuffers) completed(p []byte) {
	if len(p) < 1 {
		return
	}
	n := int(p[0])
	a.mu.Lock()
	for i := range n {
		at := 1 + i*4
		if at+4 > len(p) {
			break
		}
		handle := binary.LittleEndian.Uint16(p[at:]) & 0x0fff
		done := int(binary.LittleEndian.Uint16(p[at+2:]))
		done = min(done, a.outstanding[handle])
		a.outstanding[handle] -= done
		a.free += done
	}
	a.mu.Unlock()
	a.cond.Broadcast()
}

// forget returns a closed link's buffers: the controller flushes what it had not sent, and says so
// only by the link going away.
func (a *aclBuffers) forget(handle uint16) {
	a.mu.Lock()
	a.free += a.outstanding[handle]
	delete(a.outstanding, handle)
	a.mu.Unlock()
	a.cond.Broadcast()
}

// take waits for a free buffer and reports the packet size, or fails when ctx ends first.
func (a *aclBuffers) take(ctx context.Context, handle uint16) (int, error) {
	// Broadcast under the lock, or it can land between the check and the Wait and be lost.
	stop := context.AfterFunc(ctx, func() {
		a.mu.Lock()
		a.cond.Broadcast()
		a.mu.Unlock()
	})
	defer stop()

	a.mu.Lock()
	defer a.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if a.free > 0 && a.size > 0 {
			break
		}
		a.cond.Wait()
	}
	a.free--
	a.outstanding[handle]++
	return a.size, nil
}

// giveBack undoes a take whose packet never reached the controller.
func (a *aclBuffers) giveBack(handle uint16) {
	a.mu.Lock()
	a.free++
	a.outstanding[handle]--
	a.mu.Unlock()
	a.cond.Broadcast()
}

// sendL2CAP writes one L2CAP frame on a link, cut to the controller's buffer size.
func (r *Radio) sendL2CAP(ctx context.Context, handle, cid uint16, payload []byte) error {
	frame := make([]byte, 4, 4+len(payload))
	binary.LittleEndian.PutUint16(frame, uint16(len(payload)))
	binary.LittleEndian.PutUint16(frame[2:], cid)
	frame = append(frame, payload...)

	flags := uint16(pbFirstNonFlushable)
	for len(frame) > 0 {
		size, err := r.acl.take(ctx, handle)
		if err != nil {
			return err
		}
		n := min(size, len(frame))

		pkt := make([]byte, 5, 5+n)
		pkt[0] = h4ACL
		binary.LittleEndian.PutUint16(pkt[1:], handle|flags)
		binary.LittleEndian.PutUint16(pkt[3:], uint16(n))
		pkt = append(pkt, frame[:n]...)
		if err := r.write(pkt); err != nil {
			r.acl.giveBack(handle)
			return err
		}
		frame = frame[n:]
		flags = pbContinuation
	}
	return nil
}
