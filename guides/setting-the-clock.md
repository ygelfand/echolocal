# Setting the clock

Your Dot keeps its own clock right over the network. This is what it does and how to point it at a
time server of your choosing.

## What happens on its own

Once the Dot is on wifi it asks a time server, sets its clock, and checks again every hour. It also
writes the time into the hardware clock, so after a reboot it starts with the right time rather
than in 2010 as it used to.

On the Dot's device page in Home Assistant, under Diagnostic, the `Clock source` sensor tells you
which server it used, where it found it, and how far the clock was out. Something like:

```
192.168.1.1 (dhcp), slewed 3ms
```

The `Sync clock` button under Configuration asks it to check now.

## Where it looks for a server

1. Whatever your DHCP server hands out as an NTP server. Most routers and DHCP servers can be told
   to offer one; it is DHCP option 42, sometimes labelled "NTP servers" or "time servers". This is
   the tidiest option, because the Dot follows your network's settings with no configuration of its
   own.
2. Any servers you name in the Dot's configuration file. Add a section to
   `/system/etc/echolocal/echod.yaml`:

   ```yaml
   ntp:
     servers:
       - 192.168.1.1
       - time.example.net
   ```

   The file lives on the read-only system partition. Over adb: `echod tools remount rw`, edit the
   file, `echod tools remount ro`, then restart echod or press `Sync clock`.
3. If neither names a server, the public pools: `pool.ntp.org`, then `time.cloudflare.com`, then
   `time.google.com`.

A Dot on a network without internet access needs one of the first two, or a firewall rule that lets
UDP port 123 out to the public pools.

## If the clock is wrong

- Check `Clock source`. `unsynced` means no server has answered since the Dot started.
- If it says a public pool but you expected your own server, your DHCP server is not offering option
  42, or the Dot has not renewed its lease since that was set. A reboot renews it.
- The log (`echoctl logs`) has a `clock synced` line for every successful round and a `clock sync
  failed` warning for a round in which nobody answered.
