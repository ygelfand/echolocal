//go:build voice

package parts

import _ "embed"

//go:embed payload/echolocal-asr
var speech []byte

func Speech() []byte { return speech }
