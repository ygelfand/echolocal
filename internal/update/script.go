package update

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/ygelfand/echolocal/internal/layout"
)

// The boot hook. Amazon's two animation scripts each run a ledctrl that waits forever on a binder
// service echod does not publish, so the installer replaces both — and the one init runs at boot is
// also the only root that exists before echod, which makes it the only thing that can put echod back.
//
// It is defined here and written from two places: the installer over adb at install time, and echod
// itself on every start where the content differs. That second one is what lets this change without a
// reinstall, and it is why the definition may not be duplicated.

// Rollback is the boot hook, for the script init runs on the way up.
var Rollback = fmt.Sprintf(`#!/system/bin/sh
# Installed by EchoLocal, replacing a ledctrl call that waits forever on a binder service echod does
# not publish. echod drives the ring instead.
#
# It also puts back the binary an update replaced. echod renames %[1]s away once it has run long enough
# to be believed, so finding one here means a boot happened while an update was still on trial.
if [ -f %[1]s ]; then
    WAS=$(cat %[3]s 2>/dev/null)
    log -t echolocal "rolling back to the previous echod: an update did not settle (${WAS:-unknown})"
    mount -o remount,rw /system
    mv -f %[1]s %[2]s
    mount -o remount,ro /system
    rm -f %[3]s
    setprop %[4]s "${WAS:-1}"
fi
exit 0
`, layout.PrevBinary, layout.Binary, layout.UpdatingPath, layout.RolledBackProp)

// Stub is the boot hook for the script that runs at the end of the boot, which has nothing to do.
var Stub = `#!/system/bin/sh
# Installed by EchoLocal, replacing a ledctrl call that waits forever on a binder service echod does
# not publish. echod drives the ring instead.
exit 0
`

// Script is what belongs at one of the paths the installer takes over.
//
// Only the hook init runs on the way up carries the rollback. The other fires on
// amazon.headless.BOOT_COMPLETED, a minute or so into the boot, while a trial is not committed until the
// first heartbeat several minutes later — a rollback there would tear out an update that was doing
// nothing wrong.
func Script(path string) string {
	if path == layout.StartAnimation {
		return Rollback
	}
	return Stub
}

// Ensure writes the boot hooks when they are not already what this build expects, which is how the
// rollback improves without anybody reinstalling. Nothing is written when nothing differs, so an
// ordinary start does not touch /system at all.
func Ensure() {
	var stale []string
	for _, path := range layout.AnimationScripts {
		want := Script(path)
		if have, err := os.ReadFile(path); err == nil && string(have) == want {
			continue
		}
		stale = append(stale, path)
	}
	if len(stale) == 0 {
		return
	}

	if err := writable(true); err != nil {
		slog.Error("remounting to update the boot hooks failed", "err", err)
		return
	}
	defer func() {
		if err := writable(false); err != nil {
			slog.Error("remounting read-only failed", "err", err)
		}
	}()

	for _, path := range stale {
		if err := write(path, Script(path)); err != nil {
			slog.Error("updating a boot hook failed", "path", path, "err", err)
			continue
		}
		slog.Info("boot hook updated", "path", path)
	}
}

// write replaces a hook and labels it, because a file created here carries whatever the directory gave
// it and init has to be able to exec it.
//
// A label that will not take is logged rather than failing the write: the file is already correct, the
// kernel is permissive so nothing is denied on it anyway, and a hook with the wrong label still beats no
// hook at all.
func write(path, content string) error {
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		return err
	}
	if out, err := exec.Command("chcon", layout.OurLabel, path).CombinedOutput(); err != nil {
		slog.Warn("labelling a boot hook failed", "path", path, "err", err, "output", string(out))
	}
	return nil
}
