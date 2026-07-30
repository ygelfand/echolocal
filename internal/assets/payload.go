//go:build payload

package assets

import _ "embed"

// Staged by `make payload` into a gitignored directory, so the binaries are never committed and a
// build without the tag does not need them to exist.

//go:embed payload/echod
var echod []byte

//go:embed payload/boot.img
var bootImage []byte
