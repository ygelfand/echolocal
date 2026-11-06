package access

import (
	"fmt"
	"log/slog"
	"strconv"
	"sync"

	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/system/access"

	"github.com/ygelfand/echolocal/internal/android/firewall"
	"github.com/ygelfand/echolocal/internal/android/prop"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
)

func init() {
	component.Register(
		sharedcomponent.Device,
		func() componentAccess { return componentAccess{Get()} },
	)
}

const (
	port    = "service.adb.tcp.port"
	off     = "-1"
	adbPort = 5555

	service = "adbd"
)

type componentAccess struct{ *access.Access }

// init sets service.adb.tcp.port to 5555 on every boot.
func (c componentAccess) Restore(cfg config.Config) { c.Access.Restore(cfg.Access) }

var get = sync.OnceValue(func() *access.Access {
	a := access.New(access.Options{Settings: config.AccessSection, ADB: apply})
	component.Settings.Add(a.Controls())
	return a
})

func Get() *access.Access { return get() }

// ro.secure is 0: adb over TCP is an unauthenticated root shell.
func apply(on bool) error {
	if err := reachable(on); err != nil {
		return err
	}

	value := off
	if on {
		value = strconv.Itoa(adbPort)
	}
	if at, err := prop.Get(port); err == nil && (at == value || !on && at == "") {
		return nil
	}

	if err := prop.Set(port, value); err != nil {
		return fmt.Errorf("setting %s: %w", port, err)
	}
	if err := prop.Restart(service); err != nil {
		return fmt.Errorf("restarting %s: %w", service, err)
	}

	if on {
		slog.Warn(
			"adb is listening on the network: anything that can reach this device has a root shell",
			"port",
			adbPort,
		)
	} else {
		slog.Info("adb is back to the cable only")
	}
	return nil
}

func reachable(on bool) error {
	if on {
		if err := firewall.Open(firewall.ADB, adbPort); err != nil {
			return fmt.Errorf("opening the adb port: %w", err)
		}
		return nil
	}
	if err := firewall.Close(firewall.ADB); err != nil {
		return fmt.Errorf("closing the adb port: %w", err)
	}
	return nil
}
