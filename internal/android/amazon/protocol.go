package amazon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	msgWake         byte = 1
	msgAudio        byte = 2
	msgStartCapture byte = 3
	msgStopCapture  byte = 4
	msgPlay         byte = 5
	msgPlayStop     byte = 6

	maxPayload = 1024 * 1024
)

// Wake is the metadata the Android helper attaches to an external wake event. Only Phrase decides
// routing; the remaining fields are diagnostic and may be zero on older helper builds.
type Wake struct {
	Phrase     string
	Confidence uint32
	StartedAt  uint64
	DetectedAt uint64
}

func frame(kind byte, payload []byte) ([]byte, error) {
	if len(payload) > maxPayload {
		return nil, fmt.Errorf("amazon: payload is %d bytes, max %d", len(payload), maxPayload)
	}
	out := make([]byte, 5+len(payload))
	out[0] = kind
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[5:], payload)
	return out, nil
}

func readFrame(r io.Reader) (byte, []byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(header[1:])
	if n > maxPayload {
		return 0, nil, fmt.Errorf("amazon: frame is %d bytes, max %d", n, maxPayload)
	}
	payload := make([]byte, int(n))
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return header[0], payload, nil
}

func decodeWake(payload []byte) (Wake, error) {
	r := bytes.NewReader(payload)
	var n uint16
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return Wake{}, fmt.Errorf("amazon: wake phrase length: %w", err)
	}
	if int(n) > r.Len() || n == 0 || n > 128 {
		return Wake{}, fmt.Errorf("amazon: invalid wake phrase length %d", n)
	}
	phrase := make([]byte, int(n))
	if _, err := io.ReadFull(r, phrase); err != nil {
		return Wake{}, err
	}

	w := Wake{Phrase: string(phrase)}
	// The deployed protocol carries these fields. Tolerating their absence keeps event routing
	// compatible with the earliest helper while still rejecting a partially encoded field.
	if r.Len() == 0 {
		return w, nil
	}
	if r.Len() != 20 {
		return Wake{}, fmt.Errorf("amazon: wake metadata is %d bytes, want 20", r.Len())
	}
	if err := binary.Read(r, binary.BigEndian, &w.Confidence); err != nil {
		return Wake{}, err
	}
	if err := binary.Read(r, binary.BigEndian, &w.StartedAt); err != nil {
		return Wake{}, err
	}
	if err := binary.Read(r, binary.BigEndian, &w.DetectedAt); err != nil {
		return Wake{}, err
	}
	return w, nil
}
