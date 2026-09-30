// aspprobe tries asp.Load on a directory of vendor EQ files and reports the bucket count and which
// bucket a given volume fraction would pick. A second optional argument is the volume fraction,
// default 0.5.
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/ygelfand/echolocal/internal/lib/asp"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: aspprobe <dir> [volume-fraction]")
		os.Exit(2)
	}
	frac := 0.5
	if len(os.Args) >= 3 {
		f, err := strconv.ParseFloat(os.Args[2], 64)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bad fraction:", err)
			os.Exit(2)
		}
		frac = f
	}
	t, err := asp.Load(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "asp.Load:", err)
		os.Exit(1)
	}
	chains, err := t.Chains(1024)
	if err != nil {
		fmt.Fprintln(os.Stderr, "t.Chains:", err)
		os.Exit(1)
	}
	bucket := t.BucketFor(frac)
	fmt.Printf("loaded: %d buckets, bucket@%.2f = %d\n", len(chains), frac, bucket)
}
