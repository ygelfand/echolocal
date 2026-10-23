# Opt-in wake diagnostics

Wake diagnostics help answer whether an unexpected activation came from a model
score, a button, an API request, or a follow-up. They do not adjust the wake word,
model, threshold, microphone gain, AEC, or conversation behavior. They are a
troubleshooting tool, not a claim of improved recognition accuracy.

## Enable and retrieve

The `wakeobserve` component registers three response-capable actions through the
existing action registry. Use the paired ESPHome native API, or the existing
authenticated web action API. No additional listener or unauthenticated download
route is added. Clients with access to those control interfaces can enable
recording and retrieve recordings; this feature does not introduce per-person
permissions within a shared Home Assistant installation.

| Action | Arguments | Result |
| --- | --- | --- |
| `wake_observe_control` | `operation`: `start`, `stop`, or `status`; `hours`: 1–24; `limit`: 1–100 | Versioned status. `hours` and `limit` are used only by `start`. |
| `wake_observe_events` | None | Version, event IDs and status. |
| `wake_observe_data` | `id`: a listed ID; `kind`: `json` or `wav`; `page`: zero-based integer | Version, ID, kind, page count and page bytes. JSON encodes the bytes as base64. |

Example argument objects (pass them through your authenticated action client):

```json
{"operation":"start","hours":1,"limit":10}
```

```json
{"operation":"stop"}
```

Retrieve `json` metadata and, for model events, `wav` pages from page 0 through
`pages - 1`. Base64-decode each page separately and concatenate in order. Compare
the reconstructed WAV's SHA-256 against `wav_sha256` in its metadata. Pages are
at most 32 KiB before base64 encoding. Invalid IDs, kinds and page numbers are
rejected. Non-model markers have JSON metadata only.

Starting an active or still-draining session fails rather than extending it.
`status` and reads do not enable recording. The session is memory-only and is
disabled on restart; existing completed files remain readable.

## What is captured

For the two assistant wake slots, a model crossing can retain up to five seconds
of pre-roll and one second of post-roll from the detector's input (16 kHz mono,
signed 16-bit PCM). If observation just started, pre-roll may be shorter. The
trigger position is a frame boundary, not a word-aligned timestamp.

Metadata includes the model ID/type/file SHA-256, inference window settings,
microphone settings, playing/muted flags, threshold, crossing score, completed
hold peak when available, post-window peak, score samples, and drop counters.
Model hashing occurs at load time, outside the per-frame path. The hash identifies
the selected model file, not every shared runtime/backend dependency. Unknown or
unreadable model files cannot produce paired captures.

Fixed non-audio categories are `api_subscribed`, `api_unsubscribed`,
`wake_requested`, `button_tap`, `button_hold`, `turn_start`, and `follow_up`.
An API wake request or button press is a request marker, not proof that a turn
was accepted. Model events use slots 1–2 in JSON; API subscription markers use
slot 0. The stop-word slot is not recorded.

## Bounds and privacy

- Off by default and after process/device restart. Explicit activation lasts at
  most 24 hours and accepts at most 100 model events plus 256 non-audio markers.
- No continuous audio file is written. Up to five seconds remain in a RAM ring
  while active; only triggered captures are queued for storage.
- Pause/mute frames, model changes, source drop-counter changes, and observer
  frame gaps clear pre-roll and discard unfinished captures. Expiry and stop
  clear pending audio. Completed captures already queued may still be saved.
- Four pending captures and four queued writes maximum. Contention or a full
  queue drops diagnostics instead of waiting on storage from the audio path.
- Files live under `StateDir/wake-observation-v1`, directory mode `0700` and file
  mode `0600`. The writer reserves space below 64 MiB and 1,000 directory entries;
  on a storage error observation stops. Existing files are never overwritten or
  automatically pruned. A failed write may leave an incomplete file; it counts
  toward the storage bound. Transfer and remove unneeded captures deliberately
  during local maintenance before starting another session when storage is full.
- Capture expiry stops recording; **it does not erase existing files**.
- No automatic cloud upload. A requested download deliberately sends audio to
  the authenticated client. Recordings can contain household speech and nearby
  media: get consent before enabling, keep downloads private, and do not attach
  raw recordings or credentials to public issues by default.
- Transcripts, replies, room names, network addresses, pairing keys and passwords
  are not added to event metadata. The selected model ID and microphone settings
  are included because they are needed to interpret scores.

Diagnostic loss can make evidence incomplete. Replaying a clip with an empty
model state cannot reproduce every live inference history, and post-processing
its volume does not simulate changing the physical microphone or AEC. Check
fresh positive as well as negative examples before considering separate tuning.

## Development tests

```sh
go test ./internal/wakecapture ./internal/feature/wakeobserve
go test -race ./internal/wakecapture ./internal/feature/wakeobserve
```

Tests generate samples and model bytes in temporary directories. They cover
exact pre/post boundaries, disabled/muted operation, expiry and limits, gaps,
model changes, bounded queues, concurrent controls, in-flight writes, storage failures, restart
behavior, symlink/path rejection, page reconstruction and action validation.
No household recording, private path, model download or device is required.
