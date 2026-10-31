//go:build pryon

package parts

import _ "embed"

//go:embed payload/echolocal-pryon
var pryon []byte

func Pryon() []byte { return pryon }
