package update

import (
	"fmt"
	"syscall"
)

// selinuxAttr is where a file's SELinux label lives.
const selinuxAttr = "security.selinux"

// copyLabel gives one file the SELinux label of another.
//
// init computes a service's domain from the label of the binary it execs, and refuses to start one
// that yields no transition. Which label that has to be differs by firmware, so it is taken from the
// binary being replaced rather than named here.
func copyLabel(from, to string) error {
	label := make([]byte, 256)

	n, err := syscall.Getxattr(from, selinuxAttr, label)
	if err != nil {
		return fmt.Errorf("update: reading the label of %s: %w", from, err)
	}
	if err := syscall.Setxattr(to, selinuxAttr, label[:n], 0); err != nil {
		return fmt.Errorf("update: labelling %s: %w", to, err)
	}
	return nil
}
