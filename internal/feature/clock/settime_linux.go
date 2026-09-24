package clock

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// Setting the clock is the kernel's business, and three calls of it: settimeofday to step, adjtimex to
// slew, and an ioctl on the RTC so the time survives a reboot. Only the first of these is what an
// ordinary program ever does; the other two are why this file exists.

const (
	// adjOffsetSingleshot is adjtimex's ADJ_OFFSET_SINGLESHOT: apply this offset gradually, at the
	// kernel's slew rate of half a millisecond per second, and report what is still to go.
	adjOffsetSingleshot = 0x8001

	// The kernel clamps a single-shot offset to half a second either way. Anything more is stepped.
	maxSlew = 500 * time.Millisecond

	rtcDevice = "/dev/rtc0"

	// rtcSetTime is RTC_SET_TIME: _IOW('p', 0x0a, struct rtc_time), which is 36 bytes.
	rtcSetTime = 0x4024700a
)

// rtcTime is the kernel's struct rtc_time.
type rtcTime struct {
	sec, min, hour, mday, mon, year, wday, yday, isdst int32
}

// step sets the clock outright.
func step(to time.Time) error {
	tv := syscall.NsecToTimeval(to.UnixNano())
	if err := syscall.Settimeofday(&tv); err != nil {
		return fmt.Errorf("settimeofday: %w", err)
	}
	return nil
}

// slew asks the kernel to work the offset off gradually, so nothing watching the clock sees it jump.
func slew(offset time.Duration) error {
	tx := syscall.Timex{Modes: adjOffsetSingleshot}
	// The offset field is 32 bits on arm and 64 on arm64 and the build host; the type is inferred.
	setOffset(&tx.Offset, int64(offset/time.Microsecond))
	if _, err := syscall.Adjtimex(&tx); err != nil {
		return fmt.Errorf("adjtimex: %w", err)
	}
	return nil
}

// writeRTC puts the time in the hardware clock, which is what the kernel reads at the next boot.
// Nothing else on the device does this once Amazon's daemons are gone, and without it every boot
// starts in 2010 until the first sync lands.
func writeRTC(t time.Time) error {
	f, err := os.OpenFile(rtcDevice, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	u := t.UTC()
	rt := rtcTime{
		sec:  int32(u.Second()),
		min:  int32(u.Minute()),
		hour: int32(u.Hour()),
		mday: int32(u.Day()),
		mon:  int32(u.Month()) - 1,
		year: int32(u.Year()) - 1900,
		wday: int32(u.Weekday()),
		yday: int32(u.YearDay()) - 1,
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), rtcSetTime, uintptr(unsafe.Pointer(&rt))); errno != 0 {
		return fmt.Errorf("RTC_SET_TIME: %w", errno)
	}
	return nil
}

func setOffset[T ~int32 | ~int64](dst *T, v int64) { *dst = T(v) }
