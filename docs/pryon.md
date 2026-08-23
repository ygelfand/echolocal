# Native Amazon wake-word support

EchoLocal can use the Pryon detector already present in compatible second-generation Echo Dot
firmware. This adds **Alexa** to the wake-word select exposed through ESPHome and Home Assistant.
The detector is wake-only: EchoLocal continues to own audio capture after the wake, Home Assistant
Assist, LEDs, TTS, media, ducking and Sendspin.

## Requirements

- an Echo Dot 2 (`biscuit`) unlocked with TWRP recovery;
- the original firmware files that supply Amazon's SpeechInteractionManager, Pryon libraries and
  locale models;
- local Wi-Fi for ESPHome/Home Assistant discovery;
- Windows PowerShell 5.1 or later, Go, ADB/Fastboot, Java and an Android SDK when building from the
  source tree.

Amazon account registration is not required after the device is unlocked. EchoLocal does not ship,
copy off the device or redistribute Amazon binaries or model files.

## Complete source-tree installation

Connect one unlocked Dot by USB, then run:

```powershell
.\provision-echo-dot.ps1 -Name "Kitchen Echo"
```

Use `-Serial G090XXXXXXXXXXXX` when more than one ADB/Fastboot device is attached. The script:

1. selects only the requested `biscuit` device and verifies its firmware and boot image;
2. builds the EchoLocal daemon, Android media helper and Pryon companion;
3. saves a gitignored rollback snapshot;
4. obtains root with the verified EchoLocal boot image when required;
5. discovers the required Amazon files on the attached Dot;
6. installs EchoLocal, reboots when Android must scan the privileged companion, and restores
   `/system` read-only;
7. verifies the ESPHome API, mDNS identity, Android media bridge, Pryon frame counter and shared
   live microphone path; and
8. saves a private Home Assistant credential receipt in the gitignored rollback directory and
   prints the device's unique ESPHome encryption key last.

`-SkipBuild` reuses already built local payloads. `-BuildOnly` builds and validates the installer
without touching a device.

The equivalent lower-level command is:

```sh
echoctl install --name kitchen-echo --reboot
```

Use `--no-pryon` only when deliberately installing the legacy direct-ALSA audio path.

## Home Assistant

Add the discovered EchoLocal ESPHome device and paste the encryption key printed by the installer.
The same key is saved as `home-assistant-credentials.txt` inside the rollback snapshot reported by
the script, so it can be recovered after the terminal closes. Keep that file private.
Choose **Alexa** in the desired assistant's Wake word select. Bundled microWakeWord choices and
downloaded openWakeWord/microWakeWord models remain available in other assistant slots.

Only **Alexa** is exposed by this integration. Some Amazon firmware images also contain native
models for `Amazon`, `Computer` or `Echo`, but changing Pryon models requires restarting its Android
process. Those words should be added as a separate, tested change rather than inferred from files
that may differ by firmware and locale.

## Architecture and security boundary

The privileged `com.echolocal.pryon` APK initializes Amazon's on-device detector and sends bounded
wake metadata to EchoLocal. Pryon's native service owns the single privileged `AudioRecord` and its
Amazon `AudioStream`; EchoLocal's Android media helper reads conversation PCM through a second
authenticated reader on that same stream. This avoids the Fire OS single-input race caused by two
independent recorders. The helper continues to own `AudioTrack` playback. EchoLocal authenticates
the companion process by Android UID before accepting wake events. No audio is sent to Amazon or
another remote service by this integration.

## Verification and rollback

`echoctl status` reports the ESPHome API, Android media bridge and Pryon configuration. A successful
install requires `PRYON_CAPTURE_ACTIVE`, `PRYON_SHARED_PCM_FIRST_FRAME` and `PRYON_READY`; speaking
“Alexa” should then produce a wake event and start the selected Home Assistant Assist pipeline.
The cyan startup walk waits up to 60 seconds for Home Assistant to subscribe to a voice pipeline,
then fades out rather than looking like a recovery or boot loop.

The companion owns `/system/priv-app/EchoLocalPryon`, while the media helper and Pryon UID marker are
stored under `/data/misc/echolocal`. To roll back, force-stop `com.echolocal.pryon`, remove only the
EchoLocal-owned companion directory while `/system` is writable, restore the previous EchoLocal
files from the installer's snapshot, remount `/system` read-only and reboot. Never remove or replace
Amazon's SpeechInteractionManager, native libraries or model directories.
