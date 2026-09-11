//go:build !linux

package update

func copyLabel(from, to string) error { return nil }
