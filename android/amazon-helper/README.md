# EchoLocal Android media helper

This API-22 `app_process32` helper preserves the protocol used by the currently deployed
EchoLocal Android-media build: 16 kHz PCM capture, 48 kHz stereo playback, and wake-event
delivery. It adds a separate abstract socket named `echolocal-pryon`.

The Pryon socket accepts bounded version-1 JSON only from the Android UID recorded in
`/data/misc/echolocal/pryon.uid`, verified with `LocalSocket.getPeerCredentials()`. A valid
Alexa event is converted to the helper's existing `MSG_WAKE` frame. No audio crosses the
Pryon socket, and logcat is not used as an event transport.

Build on Windows with `./build.ps1`. Generated artifacts stay under ignored `build/`.
