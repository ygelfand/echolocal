//go:build board_cronos

package mic

import "github.com/ygelfand/echolocal/internal/lib/subband"

// cronos shares checkers' mics, but its vendor folder hasn't been checked.
var tunings []subband.Tuning
