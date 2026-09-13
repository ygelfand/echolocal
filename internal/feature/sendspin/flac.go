package sendspin

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/mewkiz/flac"

	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

// chunkFeed is the reader the parser pulls from. feed hands over one chunk and returns only once the
// parser has taken all of it and come back for more, which is the parser saying it cannot finish another
// frame out of what it has. Waiting for that is what keeps a frame's placement off the scheduler: by the
// time feed returns, every frame the chunk completed is already queued.
type chunkFeed struct {
	mu     sync.Mutex
	ready  *sync.Cond // there are bytes to read, or the feed is done
	hungry *sync.Cond // the parser is waiting on bytes that have not arrived

	pending []byte
	wants   bool
	closed  bool
}

func newChunkFeed() *chunkFeed {
	f := &chunkFeed{}
	f.ready = sync.NewCond(&f.mu)
	f.hungry = sync.NewCond(&f.mu)
	return f
}

func (f *chunkFeed) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for len(f.pending) == 0 && !f.closed {
		f.wants = true
		f.hungry.Broadcast()
		f.ready.Wait()
	}
	if len(f.pending) == 0 {
		return 0, io.EOF
	}

	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	return n, nil
}

func (f *chunkFeed) feed(chunk []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return io.ErrClosedPipe
	}

	f.pending = chunk
	f.wants = false
	f.ready.Broadcast()

	for !f.wants && !f.closed {
		f.hungry.Wait()
	}
	if f.closed {
		return io.ErrClosedPipe
	}
	return nil
}

// stop wakes both sides for good: the parser reads EOF, and a feed waiting on a parser that has already
// given up stops waiting for a hunger that will never come.
func (f *chunkFeed) stop() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.closed = true
	f.pending = nil
	f.ready.Broadcast()
	f.hungry.Broadcast()
}

// flacDecoder bridges chunks to mewkiz/flac, which wants a stream rather than pieces of one. The header
// from stream/start goes in front of every chunk fed after it, and a goroutine parses frames out the far
// side onto queue.
type flacDecoder struct {
	feed *chunkFeed
	done chan struct{}

	mu    sync.Mutex
	queue [][]int16
	err   error
}

func newFLACDecoder(header []byte) (decoder, error) {
	if len(header) == 0 {
		return nil, fmt.Errorf("sendspin: flac needs the codec header stream/start carries")
	}

	d := &flacDecoder{
		feed: newChunkFeed(),
		done: make(chan struct{}),
	}

	go d.parse(header)
	return d, nil
}

func (d *flacDecoder) parse(header []byte) {
	defer close(d.done)
	defer d.feed.stop()

	stream, err := flac.New(io.MultiReader(bytes.NewReader(header), d.feed))
	if err != nil {
		d.failed(fmt.Errorf("sendspin: flac header: %w", err))
		return
	}

	// The stream says what it is rather than the format we asked for, and narrowing to what the speaker
	// takes is the same trade the pcm decoder makes.
	channels := int(stream.Info.NChannels)
	shift := int(stream.Info.BitsPerSample) - speaker.Bits

	for {
		// Any error ends this: end of stream, or the feed closed because the decoder was let go.
		frame, err := stream.ParseNext()
		if err != nil {
			return
		}

		pcm := make([]int16, int(frame.BlockSize)*channels)
		for i := range int(frame.BlockSize) {
			for c := range channels {
				s := frame.Subframes[c].Samples[i]
				if shift > 0 {
					s >>= shift
				}
				pcm[i*channels+c] = int16(s)
			}
		}
		d.push(pcm)
	}
}

// push holds the frame on an unbounded queue rather than a sized channel. A parser blocked handing a
// frame over would never come back for bytes, and feed would wait on it forever.
func (d *flacDecoder) push(pcm []int16) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queue = append(d.queue, pcm)
}

func (d *flacDecoder) pop() []int16 {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.queue) == 0 {
		return nil
	}
	pcm := d.queue[0]
	d.queue = d.queue[1:]
	return pcm
}

func (d *flacDecoder) failed(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.err = err
}

func (d *flacDecoder) failure() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

// decode hands back one frame, or nothing when this chunk did not finish one. Never more than one: they
// would all be placed at this chunk's timestamp, and where a frame belongs is the whole point.
func (d *flacDecoder) decode(chunk []byte) ([]int16, error) {
	if err := d.failure(); err != nil {
		return nil, err
	}

	if err := d.feed.feed(chunk); err != nil {
		if perr := d.failure(); perr != nil {
			return nil, perr
		}
		return nil, io.EOF
	}
	return d.pop(), nil
}

// close stops the parser and waits for it, so a track change does not leave a goroutine behind.
func (d *flacDecoder) close() error {
	d.feed.stop()
	<-d.done
	return nil
}
