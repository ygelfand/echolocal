// Command echod is the EchoLocal agent that runs on the Echo Dot.
package main

import "github.com/ygelfand/echolocal/internal/cli/echod"

// Set via -ldflags at build time.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func main() {
	echod.Version, echod.GitCommit, echod.BuildDate = Version, GitCommit, BuildDate
	echod.Execute()
}
