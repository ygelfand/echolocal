package sendspin

import "testing"

// The wire is little-endian signed, and the sign is the part worth testing: a 24-bit sample that is
// not sign-extended before narrowing turns quiet negative audio into full-scale positive, which is
// loud and wrong rather than subtly wrong.
func TestPCMUnpacksSignedLittleEndian(t *testing.T) {
	for _, c := range []struct {
		name  string
		depth int
		bytes []byte
		want  []int16
	}{
		{"16-bit", 16, []byte{0x00, 0x00, 0xFF, 0x7F, 0x00, 0x80, 0xFF, 0xFF}, []int16{0, 32767, -32768, -1}},

		// 24-bit narrows by a byte: 0x7FFFFF is full scale positive, 0x800000 full scale negative.
		{"24-bit", 24, []byte{0x00, 0x00, 0x00, 0xFF, 0xFF, 0x7F, 0x00, 0x00, 0x80}, []int16{0, 32767, -32768}},

		// A quiet negative sample. -256 at 24 bits is 0xFFFF00, which must stay negative.
		{"24-bit quiet negative", 24, []byte{0x00, 0xFF, 0xFF}, []int16{-1}},

		{"32-bit", 32, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xFF, 0x7F}, []int16{0, 32767}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := newPCMDecoder(c.depth, 2)
			if err != nil {
				t.Fatalf("newPCMDecoder: %v", err)
			}

			got, err := d.decode(c.bytes)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %d samples, want %d", len(got), len(c.want))
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("sample %d: got %d, want %d", i, got[i], c.want[i])
				}
			}
		})
	}
}

// A short read is a bug somewhere upstream, and playing it as if it were whole samples shifts every
// channel afterwards — so it has to be refused rather than rounded down.
func TestPCMRefusesAPartialSample(t *testing.T) {
	d, _ := newPCMDecoder(24, 2)
	if _, err := d.decode([]byte{0x01, 0x02}); err == nil {
		t.Error("two bytes were accepted as a 24-bit sample")
	}
}

func TestNoDecoderForAnUnknownCodec(t *testing.T) {
	if _, err := decoderFor("mp3", 48000, 2, 16, nil); err == nil {
		t.Error("mp3 was accepted")
	}
	// FLAC cannot start without the header, and starting anyway would decode noise.
	if _, err := decoderFor("flac", 48000, 2, 16, nil); err == nil {
		t.Error("flac was accepted with no codec header")
	}
	if _, err := newPCMDecoder(8, 2); err == nil {
		t.Error("8-bit pcm was accepted")
	}
}

// The buffer is reused between chunks, so a shorter chunk after a longer one must not leave the tail
// of the previous one visible.
func TestTheReusedBufferIsCutToLength(t *testing.T) {
	d, _ := newPCMDecoder(16, 2)

	if got, _ := d.decode([]byte{1, 0, 2, 0, 3, 0, 4, 0}); len(got) != 4 {
		t.Fatalf("first chunk: got %d samples, want 4", len(got))
	}
	got, _ := d.decode([]byte{5, 0})
	if len(got) != 1 {
		t.Fatalf("second chunk: got %d samples, want 1", len(got))
	}
	if got[0] != 5 {
		t.Errorf("got %d, want 5", got[0])
	}
}

// Opus is always 48 kHz whatever the source was, which is the whole reason it is advertised first.
// The server takes the first it can serve, so the order is what decides whether the room gets a
// lossless stream or a lossy one.
func TestLosslessIsOfferedFirst(t *testing.T) {
	offered := formats()

	want := []string{"flac", "opus", "pcm"}
	if len(offered) != len(want) {
		t.Fatalf("offered %d formats, want %d: %+v", len(offered), len(want), offered)
	}
	for i, codec := range want {
		if offered[i].Codec != codec {
			t.Errorf("offered[%d] is %q, want %q", i, offered[i].Codec, codec)
		}
	}
	for _, f := range offered {
		if f.SampleRate != 48000 || f.Channels != 2 || f.BitDepth != 16 {
			t.Errorf("offered a format the speaker cannot play: %+v", f)
		}
	}
}
