// Package safe starts goroutines that cannot take the process down with them.
package safe

import (
	"log/slog"
	"runtime/debug"
)

// Go runs f on its own goroutine, turning a panic into a log line rather than a dead process.
//
// Every goroutine needs its own recover — a panic in one cannot be caught anywhere else — and init
// discards echod's stderr, so an uncaught one looks like a silent restart. Starting the goroutine
// here rather than leaving it to the caller is what makes that impossible to forget.
func Go(what string, f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("recovered from a panic",
					"in", what, "panic", r, "stack", string(debug.Stack()))
			}
		}()
		f()
	}()
}
