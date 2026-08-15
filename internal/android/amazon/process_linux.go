//go:build linux

package amazon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/ygelfand/echolocal/internal/layout"
)

type process interface{ Stop() error }

type child struct {
	cmd  *exec.Cmd
	done chan error
}

func startProcess() (process, error) {
	cmd := exec.Command("/system/bin/app_process32", "/system/bin", "echolocal.AmazonHelper", "1")
	cmd.Env = append(os.Environ(), "CLASSPATH="+layout.AndroidMediaJar)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("amazon: starting Android media helper: %w", err)
	}
	c := &child{cmd: cmd, done: make(chan error, 1)}
	go func() { c.done <- cmd.Wait() }()
	return c, nil
}

func (c *child) Stop() error {
	if c.cmd.Process != nil {
		if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
	}
	var err error
	select {
	case err = <-c.done:
	case <-time.After(2 * time.Second):
		if killErr := c.cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			return killErr
		}
		err = <-c.done
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return err
		}
	}
	return nil
}
