package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ygelfand/echolocal/internal/host/device"
	"github.com/ygelfand/echolocal/internal/layout"
)

type pryonPaths struct {
	amazonAPK string
	alexa     string
	aed       string
}

func inspectPryon(r *run) (string, bool, error) {
	if !r.cfg.Pryon {
		return "not requested", true, nil
	}

	for _, library := range []string{
		"/system/lib/libpryon.so",
		"/system/lib/libwakewordserver_jni.so",
		"/system/lib/libaudiostream.so",
		"/system/lib/libaudiostream_jni.so",
	} {
		have, err := r.d.Exists(library)
		if err != nil {
			return "", false, err
		}
		if !have {
			return "", false, fmt.Errorf("required firmware library is missing: %s", library)
		}
	}

	path, err := packageAPK(r.d, "amazon.speech.sim")
	if err != nil {
		return "", false, fmt.Errorf("locating SpeechInteractionManager: %w", err)
	}

	language, _ := r.d.Getprop("persist.sys.language")
	country, _ := r.d.Getprop("persist.sys.country")
	locales := []string{}
	if language != "" && country != "" {
		locales = append(locales, language+"-"+country)
	}
	if language != "" {
		locales = append(locales, language)
	}
	// Fire OS often reports en-GB while the physical model is reached through an en-GB symlink.
	locales = append(locales, "en-GB", "en-US")

	var searched []string
	for _, locale := range unique(locales) {
		candidate := "/system/local/models/keyword/" + locale + "/ALEXA/pryon.manifest"
		searched = append(searched, candidate)
		if have, _ := r.d.Exists(candidate); have {
			r.pryon.alexa = candidate
			break
		}
	}
	if r.pryon.alexa == "" {
		found, _ := r.d.Shell("find /system/local/models/keyword -path '*/ALEXA/pryon.manifest' 2>/dev/null")
		r.pryon.alexa = firstLine(found)
	}
	if r.pryon.alexa == "" {
		return "", false, fmt.Errorf("unable to locate the Alexa Pryon manifest; searched: %s",
			strings.Join(searched, ", "))
	}

	r.pryon.aed = "/system/local/models/AED/pryon.manifest"
	if have, _ := r.d.Exists(r.pryon.aed); !have {
		found, _ := r.d.Shell("find /system/local/models -path '*/AED/pryon.manifest' 2>/dev/null")
		r.pryon.aed = firstLine(found)
	}
	if r.pryon.aed == "" {
		return "", false, errors.New("unable to locate the required AED Pryon manifest under /system/local/models")
	}

	r.pryon.amazonAPK = path
	return fmt.Sprintf("SIM=%s, Alexa=%s, AED=%s", path, r.pryon.alexa, r.pryon.aed), false, nil
}

// packageAPK resolves an installed package even after `pm hide`. Fire OS 5 makes `pm path` return
// exit 1 for a hidden package, while `pm list packages -f -u` still reports the system APK. Pryon is
// finalized after a reboot with Amazon's audio packages hidden, so both views are required.
func packageAPK(d *device.Device, name string) (string, error) {
	direct, directCode, err := d.ShellCode("pm path " + name)
	if err != nil {
		return "", err
	}
	if directCode == 0 {
		if path := packagePath(direct, name); path != "" {
			return path, nil
		}
	}

	all, listCode, err := d.ShellCode("pm list packages -f -u " + name)
	if err != nil {
		return "", err
	}
	if listCode == 0 {
		if path := packagePath(all, name); path != "" {
			return path, nil
		}
	}
	return "", fmt.Errorf("package %s reported no APK path (pm path exit %d, hidden-package list exit %d)",
		name, directCode, listCode)
}

func packagePath(output, name string) string {
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "package:") {
			continue
		}
		value := strings.TrimPrefix(line, "package:")
		if path, packageName, found := strings.Cut(value, "="); found {
			if strings.TrimSpace(packageName) != name {
				continue
			}
			return strings.TrimSpace(path)
		}
		if value != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

func installAndroidMedia(r *run) (string, bool, error) {
	if !r.cfg.Pryon {
		return "not requested", true, nil
	}
	if _, err := r.d.Shell("mkdir -p " + layout.StateDir); err != nil {
		return "", false, err
	}
	if same, err := sameRemote(r.d, layout.AndroidMediaJar, r.cfg.AndroidMedia); err != nil {
		return "", false, err
	} else if same {
		return "already installed", true, nil
	}
	if err := r.d.WriteFile(layout.AndroidMediaJar, r.cfg.AndroidMedia, 0o644); err != nil {
		return "", false, err
	}
	return layout.AndroidMediaJar, false, nil
}

func installPryonAPK(r *run) (string, bool, error) {
	if !r.cfg.Pryon {
		return "not requested", true, nil
	}
	if same, err := sameRemote(r.d, layout.PryonAPK, r.cfg.PryonAPK); err != nil {
		return "", false, err
	} else if same {
		return "already installed", true, nil
	}
	if _, err := r.d.Shell("mkdir -p " + layout.PryonDir); err != nil {
		return "", false, err
	}
	if err := r.d.WriteFile(layout.PryonAPK, r.cfg.PryonAPK, 0o644); err != nil {
		return "", false, err
	}
	if err := r.d.Chcon(layout.OurLabel, layout.PryonAPK); err != nil {
		return "", false, err
	}
	r.reboot = true
	return layout.PryonAPK + " (effective after reboot)", false, nil
}

func sameRemote(d *device.Device, path string, want []byte) (bool, error) {
	have, err := d.Exists(path)
	if err != nil || !have {
		return false, err
	}
	got, err := d.ReadFile(path)
	if err != nil {
		return false, err
	}
	return bytes.Equal(got, want), nil
}

func stopPryon(r *run) (string, bool, error) {
	have, err := r.d.Exists(layout.PryonAPK)
	if err != nil || !have {
		return "not installed", true, err
	}
	_, err = r.d.Shell("am force-stop " + layout.PryonPackage)
	return layout.PryonPackage, false, err
}

func removePryonAPK(r *run) (string, bool, error) {
	have, err := r.d.Exists(layout.PryonDir)
	if err != nil || !have {
		return "not installed", true, err
	}
	_, err = r.d.Shell("rm -rf " + layout.PryonDir)
	return layout.PryonDir + " (package removal settles after reboot)", false, err
}

func removeAndroidMedia(r *run) (string, bool, error) {
	paths := layout.AndroidMediaJar + " " + layout.PryonUIDPath
	_, err := r.d.Shell("rm -f " + paths)
	return paths, false, err
}

var finalizePryonSteps = []step{
	{"rediscover Pryon firmware", inspectPryon},
	{"record Pryon package UID", recordPryonUID},
	{"configure Pryon detector", configurePryon},
	{"restart echod with Android media", stopService},
	{"start echod with Alexa capability", startService},
	{"verify EchoLocal ESPHome service", verifyESPHome},
	{"verify Pryon detector", verifyPryon},
}

// FinalizePryon runs after Android has scanned the newly installed privileged APK. It pins the
// package UID used by socket authentication, persists firmware paths in the companion and verifies
// both the ESPHome-facing runtime and the native detector.
func FinalizePryon(ctx context.Context, d *device.Device, cfg Config, report Reporter) error {
	if !cfg.Pryon {
		return nil
	}
	return execute(ctx, finalizePryonSteps, &run{d: d, cfg: cfg, ctx: ctx}, report)
}

var userIDPattern = regexp.MustCompile(`(?m)\buserId=(\d+)\b`)

// PryonScanned reports whether Android's package manager has loaded the privileged companion.
// A newly copied system APK is not visible until the next boot.
func PryonScanned(d *device.Device) (bool, error) {
	dump, code, err := d.ShellCode("dumpsys package " + layout.PryonPackage)
	if err != nil {
		return false, err
	}
	return code == 0 && userIDPattern.MatchString(dump), nil
}

func recordPryonUID(r *run) (string, bool, error) {
	dump, err := r.d.Shell("dumpsys package " + layout.PryonPackage)
	if err != nil {
		return "", false, err
	}
	match := userIDPattern.FindStringSubmatch(dump)
	if len(match) != 2 {
		return "", false, fmt.Errorf("package %s has no userId after reboot", layout.PryonPackage)
	}
	uid, err := strconv.Atoi(match[1])
	if err != nil || uid < 10000 {
		return "", false, fmt.Errorf("package %s reported invalid UID %q", layout.PryonPackage, match[1])
	}
	want := []byte(strconv.Itoa(uid) + "\n")
	if same, err := sameRemote(r.d, layout.PryonUIDPath, want); err != nil {
		return "", false, err
	} else if same {
		return strconv.Itoa(uid), true, nil
	}
	if err := r.d.WriteFile(layout.PryonUIDPath, want, 0o600); err != nil {
		return "", false, err
	}
	return strconv.Itoa(uid), false, nil
}

func configurePryon(r *run) (string, bool, error) {
	cmd := fmt.Sprintf("am startservice -n %s/.PryonDetectorService --es amazon_apk %s --es alexa_model %s --es aed_model %s",
		layout.PryonPackage, r.pryon.amazonAPK, r.pryon.alexa, r.pryon.aed)
	out, err := r.d.Shell(cmd)
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(out), false, nil
}

func verifyESPHome(r *run) (string, bool, error) {
	name, err := ReadName(r.d)
	if err != nil || name == "" {
		return "", false, fmt.Errorf("EchoLocal device name is unavailable: %w", err)
	}
	if _, err := KeyOrError(r.d); err != nil && !r.cfg.ZeroPSK {
		return "", false, err
	}
	mac, err := r.d.Shell("cat " + layout.MACPath)
	if err != nil || layout.MAC(mac) == "" {
		return "", false, fmt.Errorf("EchoLocal device MAC is unavailable: %w", err)
	}
	// startService proves that init launched this binary, not that all of its components have reached
	// their listeners. The Android media helper can take several seconds to create AudioRecord and
	// AudioTrack after a cold boot, so wait for the resident state and port rather than racing them.
	readyDeadline := time.Now().Add(30 * time.Second)
	var state string
	var listening bool
	for time.Now().Before(readyDeadline) {
		state, err = r.d.Getprop(layout.StateProp)
		if err != nil {
			return "", false, err
		}
		if state == "resident" {
			port := strings.ToUpper(fmt.Sprintf("%04X", layout.Port))
			_, code, portErr := r.d.ShellCode("cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | grep ':" + port + " '")
			if portErr != nil {
				return "", false, portErr
			}
			listening = code == 0
			if listening {
				break
			}
		}
		select {
		case <-r.ctx.Done():
			return "", false, r.ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if state != "resident" || !listening {
		return "", false, fmt.Errorf("ESPHome native API not ready within 30s (echod state=%q, tcp/%d listening=%t)",
			state, layout.Port, listening)
	}

	mdnsDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(mdnsDeadline) {
		logs, _ := r.d.Shell("logcat -d -s echolocal:I '*:S'")
		if strings.Contains(logs, "advertising over mdns") {
			return fmt.Sprintf("%s, %s, %s %s/%s on tcp/%d with mDNS",
				name, layout.MAC(mac), layout.Manufacturer, layout.Model, layout.Platform, layout.Port), false, nil
		}
		select {
		case <-r.ctx.Done():
			return "", false, r.ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return "", false, errors.New("ESPHome API is listening but mDNS discovery was not advertised within 15s; verify Wi-Fi before completing installation")
}

func verifyPryon(r *run) (string, bool, error) {
	deadline := time.Now().Add(30 * time.Second)
	var logs string
	for time.Now().Before(deadline) {
		// Fire OS routes privileged-app logs to amazon_main rather than the default buffer. `-b all`
		// keeps verification independent of which Android UID emitted each half of the handshake.
		poc, _ := r.d.Shell("logcat -b all -d -s EchoLocalPryon:I '*:S'")
		echo, _ := r.d.Shell("logcat -b all -d -s echolocal:I '*:S'")
		logs = "Pryon: " + lastLines(poc, 4) + " | EchoLocal: " + lastLines(echo, 4)
		echoReady := strings.Contains(echo, "pryon=1") ||
			strings.Contains(echo, "id=pryon_alexa engine=pryon")
		if strings.Contains(poc, "PRYON_READY") && echoReady {
			return "native detector ready; Alexa advertised", false, nil
		}
		select {
		case <-r.ctx.Done():
			return "", false, r.ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return "", false, fmt.Errorf("Pryon did not report ready with Alexa installed within 30s; recent state: %s",
		strings.TrimSpace(logs))
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
