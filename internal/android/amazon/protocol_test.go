package amazon

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	want := []byte{1, 2, 3, 4}
	encoded, err := frame(msgAudio, want)
	if err != nil {
		t.Fatal(err)
	}
	kind, got, err := readFrame(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if kind != msgAudio || !bytes.Equal(got, want) {
		t.Fatalf("got type=%d payload=%v, want type=%d payload=%v", kind, got, msgAudio, want)
	}
}

func TestDecodeWake(t *testing.T) {
	var payload bytes.Buffer
	_ = binary.Write(&payload, binary.BigEndian, uint16(len("Alexa")))
	payload.WriteString("Alexa")
	_ = binary.Write(&payload, binary.BigEndian, uint32(912))
	_ = binary.Write(&payload, binary.BigEndian, uint64(123))
	_ = binary.Write(&payload, binary.BigEndian, uint64(456))

	got, err := decodeWake(payload.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got.Phrase != "Alexa" || got.Confidence != 912 || got.StartedAt != 123 || got.DetectedAt != 456 {
		t.Fatalf("decoded %+v", got)
	}
}

func TestDecodeWakeRejectsPartialMetadata(t *testing.T) {
	payload := []byte{0, 5, 'A', 'l', 'e', 'x', 'a', 0}
	if _, err := decodeWake(payload); err == nil {
		t.Fatal("partial metadata was accepted")
	}
}
