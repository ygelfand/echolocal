//go:build payload

package assets

import _ "embed"

// Staged by `make payload` into a gitignored directory, so the binary is never committed and a build
// without the tag does not need it to exist.

//go:embed payload/echod
var echod []byte
