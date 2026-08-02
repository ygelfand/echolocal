package layout

import "fmt"

// Set with -ldflags at build time.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// VersionString is what a --version flag prints.
func VersionString() string {
	return fmt.Sprintf("%s (%s, %s)", Version, GitCommit, BuildDate)
}
