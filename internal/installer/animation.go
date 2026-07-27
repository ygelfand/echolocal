package installer

import (
	"fmt"
	"strings"

	"github.com/ygelfand/echolocal/internal/layout"
)

// The boot animation is driven from two init services in /init.csm.project.rc:
//
//	service boot2_anim_start /system/bin/start_animation.sh anim_start_phase2
//	service boot2_anim_stop  /system/bin/stop_animation.sh anim_start_phase2
//
// Each script runs /system/bin/ledctrl, a client of the LedController binder service that the
// stock ledcontroller published. echod does not publish it, so the client blocks in getService
// and retries once a second for the life of the boot, holding its parent shell open.
//
// The rc file is in the boot ramdisk, but the scripts it runs are in /system, so replacing them
// is enough. echod owns the ring by the time either would run.
var animationStub = `#!/system/bin/sh
# Installed by EchoLocal, replacing a ledctrl call that waits forever on a binder service echod
# does not publish. echod drives the ring instead.
exit 0
`

// disableBootAnimation replaces both animation scripts, keeping the originals alongside.
func disableBootAnimation(r *run) (string, bool, error) {
	var done []string
	for _, path := range layout.AnimationScripts {
		replaced, err := stubScript(r, path)
		if err != nil {
			return "", false, err
		}
		if replaced {
			done = append(done, path)
		}
	}
	if len(done) == 0 {
		return "already stubbed", true, nil
	}
	return strings.Join(done, ", "), false, nil
}

// stubScript reports whether it had to write the stub.
func stubScript(r *run, path string) (bool, error) {
	current, err := r.d.ReadFile(path)
	if err == nil && string(current) == animationStub {
		return false, nil
	}

	backup := path + layout.BackupSuffix
	saved, err := r.d.Exists(backup)
	if err != nil {
		return false, err
	}
	if !saved {
		if _, err := r.d.Shell(fmt.Sprintf("mv %s %s", path, backup)); err != nil {
			return false, err
		}
	}

	if err := r.d.WriteFile(path, []byte(animationStub), 0o755); err != nil {
		return false, err
	}
	return true, r.d.Chcon(layout.OurLabel, path)
}

// restoreBootAnimation is the uninstall counterpart.
func restoreBootAnimation(r *run) (string, bool, error) {
	var done []string
	for _, path := range layout.AnimationScripts {
		backup := path + layout.BackupSuffix
		saved, err := r.d.Exists(backup)
		if err != nil {
			return "", false, err
		}
		if !saved {
			continue
		}
		if _, err := r.d.Shell(fmt.Sprintf("mv %s %s", backup, path)); err != nil {
			return "", false, err
		}
		done = append(done, path)
	}
	if len(done) == 0 {
		return "nothing to restore", true, nil
	}
	return strings.Join(done, ", "), false, nil
}
