package installer

import "testing"

// getprop prints one property per line as [name]: [value], and the state matters: a service init knows
// about but is not running must not be stopped, and a value that merely contains "running" is not one.
func TestRunningServices(t *testing.T) {
	const props = `[init.svc.adbd]: [running]
[init.svc.mixer]: [running]
[init.svc.adepd]: [stopped]
[init.svc.oobe_setup]: [restarting]
[ro.product.device]: [biscuit_puffin]
[persist.sys.usb.config]: [mtp,adb]
`

	running := runningServices(props)

	for _, want := range []string{"adbd", "mixer"} {
		if !running[want] {
			t.Errorf("%s is running and was not reported", want)
		}
	}
	for _, not := range []string{"adepd", "oobe_setup", "product.device"} {
		if running[not] {
			t.Errorf("%s is not running and was reported", not)
		}
	}
	if len(running) != 2 {
		t.Errorf("reported %v, want just the two running services", running)
	}
}
