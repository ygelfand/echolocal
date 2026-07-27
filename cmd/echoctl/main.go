// Command echoctl is the EchoLocal host CLI.
package main

import "github.com/ygelfand/echolocal/internal/cli/echoctl"

// Set via -ldflags at build time.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func main() {
	echoctl.Version, echoctl.GitCommit, echoctl.BuildDate = Version, GitCommit, BuildDate
	echoctl.Execute()
}
