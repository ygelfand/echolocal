package sendspin

import (
	"bytes"
	"fmt"
	"io"

	"github.com/mewkiz/flac"

	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

// flacDecoder bridges chunks to mewkiz/flac, which wants a stream rather than pieces of one. The header
// from stream/start goes in front of every chunk written after it, and a goroutine parses frames out the
// far side.
type flacDecoder struct {
	w      *io.PipeWriter
	frames chan []int16
	fail   chan error
}

func newFLACDecoder(header []byte) (decoder, error) {
	if len(header) == 0 {
		return nil, fmt.Errorf("sendspin: flac needs the codec header stream/start carries")
	}

	r, w := io.Pipe()
	d := &flacDecoder{
		w:      w,
		frames: make(chan []int16, 8),
		fail:   make(chan error, 1),
	}

	go d.parse(r, header)
	return d, nil
}

func (d *flacDecoder) parse(r *io.PipeReader, header []byte) {
	defer close(d.frames)
	defer r.Close()

	stream, err := flac.New(io.MultiReader(bytes.NewReader(header), r))
	if err != nil {
		d.fail <- fmt.Errorf("sendspin: flac header: %w", err)
		return
	}

	// The stream says what it is rather than the format we asked for, and narrowing to what the speaker
	// takes is the same trade the pcm decoder makes.
	channels := int(stream.Info.NChannels)
	shift := int(stream.Info.BitsPerSample) - speaker.Bits

	for {
		// Any error ends this: end of stream, or the pipe closed because the decoder was let go.
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
		d.frames <- pcm
	}
}

// decode hands back one frame, or nothing when this chunk did not finish one. Never more than one: they
// would all be placed at this chunk's timestamp, and where a frame belongs is the whole point.
func (d *flacDecoder) decode(chunk []byte) ([]int16, error) {
	select {
	case err := <-d.fail:
		return nil, err
	default:
	}

	if _, err := d.w.Write(chunk); err != nil {
		return nil, fmt.Errorf("sendspin: flac: %w", err)
	}

	select {
	case pcm, ok := <-d.frames:
		if !ok {
			return nil, io.EOF
		}
		return pcm, nil
	default:
		return nil, nil
	}
}

// close stops the parser and waits for it, so a track change does not leave a goroutine behind.
func (d *flacDecoder) close() error {
	d.w.Close()
	for range d.frames {
	}
	return nil
}
