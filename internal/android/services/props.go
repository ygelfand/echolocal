package services

import (
	"fmt"

	"github.com/ygelfand/echolocal/internal/host/device"
)

// Gated is the properties provisioning changes so that init never starts a vendor service.
//
// Hiding a package stops Android from binding it but says nothing to init, which starts native
// services regardless. SmartHomeWifid is one of those, and with its service package hidden it
// never finishes initializing and busy-polls a socket at a full core.
//
// init cannot express "and", so /init.smarthome.rc prefixes wifi.launch once per condition and
// starts the service when it reaches 111. A migration marker left at 0 means it never gets there.
var Gated = []Prop{
	{
		Name: "persist.wifi.migrate.complete", Value: "0", Stock: "1",
		Reason: "reaches wifi.launch=111, which starts SmartHomeWifid",
	},
}

// Prop is one property, the value provisioning wants, and the value a stock device holds.
type Prop struct {
	Name   string
	Value  string
	Stock  string
	Reason string
}

// Gate applies Gated. The properties persist, but init only evaluates its triggers at boot, so a
// service that is already running keeps running until the device is rebooted.
func Gate(d *device.Device) (changed []string, err error) {
	return setProps(d, func(p Prop) string { return p.Value })
}

// Ungate restores the stock values.
func Ungate(d *device.Device) (changed []string, err error) {
	return setProps(d, func(p Prop) string { return p.Stock })
}

func setProps(d *device.Device, want func(Prop) string) (changed []string, err error) {
	for _, p := range Gated {
		have, err := d.Getprop(p.Name)
		if err != nil {
			return changed, err
		}
		if have == want(p) {
			continue
		}
		if err := d.Setprop(p.Name, want(p)); err != nil {
			return changed, fmt.Errorf("setting %s: %w", p.Name, err)
		}
		changed = append(changed, p.Name)
	}
	return changed, nil
}
