//go:build !linux

package amazon

import "errors"

type process interface{ Stop() error }

func startProcess() (process, error) {
	return nil, errors.New("amazon: Android media helper is available only on the device")
}
