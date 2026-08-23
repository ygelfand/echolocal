# EchoLocal Android media helper

This API-22 `app_process32` helper preserves EchoLocal's Android-media protocol: 16 kHz PCM,
48 kHz stereo playback, and wake-event delivery. When Pryon is selected it reads live PCM from a
second reader on Pryon's firmware-owned Amazon `AudioStream`; it does not open a competing
`AudioRecord`. It also accepts wake events on a separate abstract socket named `echolocal-pryon`.

The Pryon socket accepts bounded version-1 JSON only from the Android UID recorded in
`/data/misc/echolocal/pryon.uid`, verified with `LocalSocket.getPeerCredentials()`. A valid
Alexa event is converted to the helper's existing `MSG_WAKE` frame. No audio crosses the
Pryon socket, and logcat is not used as an event transport.

The separate `echolocal-pryon-pcm` socket is local, root-authenticated and carries only the shared
16 kHz mono PCM frames from the Pryon audio provider to this helper. Audio remains on the Dot.

Build on Windows with `./build.ps1`. Generated artifacts stay under ignored `build/`.
