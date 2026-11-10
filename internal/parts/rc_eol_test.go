package parts

import (
	"bytes"
	"testing"
)

// init reads a carriage return as part of the word before it.
func TestTheInitRCHasNoCarriageReturns(t *testing.T) {
	if bytes.ContainsRune(initRC, '\r') {
		t.Error("echolocal.rc has CRLF line endings; init would misread every line")
	}
}
