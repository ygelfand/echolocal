# Keeping the clock right

How echod sets the device's time, where it looks for a time server, and why Amazon's two time daemons
are switched off. For someone changing `internal/feature/clock`, `internal/lib/sntp` or
`internal/android/lease`. The user-facing notes are in `guides/setting-the-clock.md`.

## What was there before

Fire OS ships two daemons for this. `sntpd` has its server list compiled in: four North American
pool.ntp.org hosts and an Amazon one. It tries an HTTP time source first, through Amazon's captive
portal check URL, and only then falls back to NTP. It never writes the hardware clock, so a device
boots into 2010 every time and stays there until the network is up and a query has succeeded.
`securetime` asks a trusted execution environment this board does not have for a secure time, fails
the same call every few seconds, and logs each failure. Neither reads anything the local network
offers.

Both are now disabled at install and stopped at every boot on a device that was installed earlier.

## What replaces them

`internal/feature/clock` is a component in the network phase. Once the device has an address it asks
a time server, applies the answer, writes the hardware clock, and repeats every hour. A failed round
is retried after 30 seconds, doubling to ten minutes. The `Sync clock` button in Home Assistant asks
for a round now.

### Where it looks

In order, skipping duplicates:

1. **DHCP.** dhcpcd keeps the lease as the raw DHCP message at `/data/misc/dhcp/dhcpcd-wlan0.lease`,
   and `internal/android/lease` reads option 42 out of it. The lease is read on every round, so a
   device that moves network follows it. Stock dhcpcd does not ask for that option; the installer
   adds `ntp_servers` to the option lines in `/system/etc/dhcpcd/dhcpcd.conf`, and dhcpcd picks that
   up at the reboot that ends the install.
2. **Configuration.** `ntp.servers` in `/system/etc/echolocal/echod.yaml`, a list of hosts, with a
   port if it is not 123.
3. **Defaults.** `pool.ntp.org`, `time.cloudflare.com`, `time.google.com`.

Names resolve through echod's own resolver, which reads the nameservers from the DHCP properties, so
the public pools work wherever the device has DNS.

### How the answer is applied

`internal/lib/sntp` is the client half of RFC 4330: one 48-byte packet out, one back, four
timestamps. The offset is the usual `((t2 - t1) + (t3 - t4)) / 2`. A reply is checked to be a
server reply, to echo the transmit timestamp of the question it is answering, to carry a transmit
time, and not to be a kiss-of-death (stratum 0).

What happens to the clock depends on how far out it is:

| Offset | Action |
| --- | --- |
| under 10 ms | nothing |
| up to 500 ms | slewed with `adjtimex`, at the kernel's half a millisecond per second, so nothing watching the clock sees a jump |
| more | stepped with `settimeofday` and logged at warning |

Half a second is where the kernel clamps a single-shot adjustment, so it is also where slewing stops
being possible. In practice only the first round after a boot from a stale hardware clock steps;
the hourly rounds slew by a few milliseconds or do nothing.

After every answer the time is written to `/dev/rtc0` with `RTC_SET_TIME`, so the next boot starts
within a slew of right and never has to step.

Why the gentleness matters here: Sendspin's time filter maps server time to the local wall clock,
and a step in the wall clock during playback is a jump the renderer has to snap out. Slewing keeps
that from happening in normal running.

### Platform files

`settime_linux.go` holds the three system calls. `settime_other.go` makes them report that they
only work on the device, so the package builds and its selection tests run on a development host.
The `adjtimex` offset field is 32 bits on arm and 64 on arm64, which is what the small generic
setter in the Linux file is for.

## Home Assistant entities

| Entity | Kind | What it shows |
| --- | --- | --- |
| Clock source | text sensor, diagnostic | server, where it came from (dhcp, config, default), what was done and by how much |
| Sync clock | button | asks for a round now |

## Testing

`sntp_test.go` runs a loopback server that answers with a known skew and checks the measured offset,
then mangles replies four ways and checks each is refused. `lease_test.go` builds DHCP messages and
checks option 42 is found, padding is skipped and a truncated option is clipped rather than read past
the end, and that the dhcpcd configuration edit is idempotent. `clock_test.go` checks the order the
sources are tried in. Setting the clock itself is only exercised on a device.
