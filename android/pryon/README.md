# EchoLocal Pryon wake companion

This API-22 privileged APK is deliberately limited to Pryon wake detection and wake-event delivery:

- create the firmware-owned Amazon `AudioStream` in a separate Binder process;
- initialize `NativeWakeWordServiceCore` with paths discovered on the attached Dot;
- let `libwakewordserver_jni.so` own the privileged 16 kHz HOTWORD recorder;
- expose a root-authenticated second `AudioStream` reader to EchoLocal's media helper, so live
  conversation audio and Pryon detection use the same recorder instead of racing for two inputs;
- print `PRYON_WAKEWORD_DETECTED word=alexa ...` for accepted live detections;
- deliver a bounded, versioned JSON wake event to EchoLocal's authenticated filesystem socket;
- disable and destroy the native service during an orderly shutdown.
- exit both isolated ART processes after teardown because Amazon's JNI libraries are
  process-global and cannot be safely reloaded by a second `DexClassLoader`.

It does not contain proprietary files, send audio anywhere, or invoke EchoLocal's voice,
LED, media, Home Assistant, TTS, or ducking paths. It sends only wake metadata to
the abstract socket `@echolocal-pryon`; the Android media helper authenticates the peer
UID and forwards the existing wake frame to `echod`, which owns all response behavior.

## Build on Windows

```powershell
.\build.ps1
```

The script uses the installed Android SDK, Java compiler, and the user's standard debug
keystore. Generated files remain under the ignored `build/` directory.

## Device configuration

Install the signed APK as `/system/priv-app/EchoLocalPryon/EchoLocalPryon.apk`,
reboot so Android grants system-app permissions, then supply the paths found during the
read-only device inventory:

```text
am startservice -n com.echolocal.pryon/.PryonDetectorService \
  --es amazon_apk /system/priv-app/SpeechInteractionManager/SpeechInteractionManager.apk \
  --es alexa_model /system/local/models/keyword/en-GB/ALEXA/pryon.manifest \
  --es aed_model /system/local/models/AED/pryon.manifest
```

All three paths are required together, validated as readable absolute files, and persisted
for restart/reboot tests. The source contains no firmware-specific proprietary path default.

Observe only the companion tag:

```text
adb logcat -v time -s EchoLocalPryon:I '*:S'
```

Startup verification requires `PRYON_CAPTURE_ACTIVE`, `PRYON_SHARED_PCM_FIRST_FRAME` and
`PRYON_READY`, proving that the native frame counter and EchoLocal's shared reader both receive live
microphone audio. Speaking near the physical Dot should then produce repeated deterministic
`PRYON_WAKEWORD_DETECTED word=alexa` lines.
WAV injection is not accepted as proof of live microphone operation.

## Rollback boundary

The only installed system path owned by this companion is:

```text
/system/priv-app/EchoLocalPryon
```

Rollback must force-stop `com.echolocal.pryon`, remove exactly that directory while
`/system` is writable, remount `/system` read-only, and reboot. Do not remove or replace any
Amazon APK, native library, or model.
