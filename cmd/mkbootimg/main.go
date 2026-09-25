// Command mkbootimg patches the kernel command line of an Android boot image so
// the kernel boots with androidboot.selinux=permissive.
//
// Usage:
//
//	mkbootimg <input.img> <output.img> ["new cmdline"]
//
// Without a new cmdline it appends androidboot.selinux=permissive to the
// existing one. With one it replaces it entirely.
//
// The cmdline lives at offset 64 in the boot header, in a 512-byte NUL-padded
// region. Padding past the string is zeros, so what is appended is just
// whatever is already there plus the new flag.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	cmdlineOffset = 64
	cmdlineSize   = 512
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: mkbootimg <input> <output> [new-cmdline]\n")
		os.Exit(2)
	}
	in, out := os.Args[1], os.Args[2]
	var newCmd string
	if len(os.Args) > 3 {
		newCmd = os.Args[3]
	}

	if err := patch(in, out, newCmd); err != nil {
		fmt.Fprintln(os.Stderr, "mkbootimg:", err)
		os.Exit(1)
	}
}

func patch(in, out, newCmd string) error {
	data, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	if len(data) < cmdlineOffset+cmdlineSize {
		return fmt.Errorf("file is %d bytes, smaller than the header", len(data))
	}
	if !bytes.HasPrefix(data, []byte("ANDROID!")) {
		return errors.New("not an Android boot image (missing ANDROID! magic)")
	}

	cur := currentCmdline(data)
	if newCmd == "" {
		if strings.Contains(cur, "androidboot.selinux=permissive") {
			fmt.Fprintf(os.Stderr, "mkbootimg: cmdline already has androidboot.selinux=permissive, writing anyway\n")
		} else {
			newCmd = strings.TrimRight(cur, " ") + " androidboot.selinux=permissive"
		}
	}
	if newCmd == "" {
		return errors.New("refusing to write an empty cmdline")
	}
	if len(newCmd)+1 > cmdlineSize {
		return fmt.Errorf("cmdline is %d bytes, header only has %d", len(newCmd)+1, cmdlineSize)
	}

	// Wipe the region, write the new string, NUL-terminate, then re-zero the rest.
	for i := cmdlineOffset; i < cmdlineOffset+cmdlineSize; i++ {
		data[i] = 0
	}
	copy(data[cmdlineOffset:], newCmd)
	data[cmdlineOffset+len(newCmd)] = 0

	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}

	sum := sha256.Sum256(data)
	fmt.Printf("wrote %s (%d bytes, sha256=%s)\n", out, len(data), hex.EncodeToString(sum[:]))
	fmt.Printf("cmdline: %q\n", newCmd)
	return nil
}

func currentCmdline(data []byte) string {
	raw := data[cmdlineOffset : cmdlineOffset+cmdlineSize]
	if end := bytes.IndexByte(raw, 0); end >= 0 {
		return string(raw[:end])
	}
	return string(raw)
}
