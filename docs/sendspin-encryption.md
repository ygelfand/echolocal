# Sendspin encryption and pairing

This describes how echod speaks the encrypted Sendspin protocol, what it stores, and the decisions
behind it. It is written for someone changing the code in `internal/feature/sendspin`. If you just want
to pair a Dot with Music Assistant, read the guide in `guides/pairing-with-music-assistant.md` instead.

## Why this exists

The Sendspin spec made encryption mandatory in June 2026. Every connection now runs a Noise KKpsk2
handshake inside the WebSocket, and pairing gives each server a long-term key for the room. Music
Assistant labels a room that does not do this as "connected without encryption (legacy mode)" and
only admits it while its "Allow legacy clients" option is on, which its documentation describes as
temporary.

echod used to speak the older cleartext protocol through the client in the sendspin-go library. That
library's tagged releases have no encryption, and its unreleased work is behind the spec in several
places, so the protocol layer now lives in this repository. Only the clock filter from
`github.com/Sendspin/sendspin-go/pkg/sync` is still used.

## The files

| File | What it does |
| --- | --- |
| `secure.go` | Identities, pre-shared keys, the Noise responder, the transport, and frame reassembly. |
| `trust.go` | The trust file: identity, pairing key, pairing records, and the token. |
| `conn.go` | One socket: the cleartext preamble, re-handshakes, encrypted reads and writes. |
| `messages.go` | The JSON messages, as the spec shapes them. |
| `admission.go` | The rules for which `server/activate` a key permits, and how to refuse one. |
| `session.go` | One server's connection from open to close: establishment, pairing, playback, clock sync. |
| `listen.go` | The port a server dials in to. One server at a time. |
| `sendspin.go` | The Home Assistant entities and the component lifecycle. |

Decoding and speaker output (`decode.go`, `flac.go`, `output.go`) are unchanged from before.

## The handshake

The server is always the Noise initiator and the room the responder, whichever side opened the
socket. The exchange is:

1. Room sends `client/init` as a text frame: its identity, version 1, and the cipher suite. echod
   always picks `25519_ChaChaPoly_SHA256`.
2. Server sends `server/init` with its identity.
3. Server sends Noise message 1 in a `noise/handshake` envelope. Its encrypted payload names the
   pre-shared key it wants to use by `psk_id`, and on newer servers the key's category.
4. Room answers with Noise message 2, and both sides switch to encrypted binary frames.

The exact bytes of the two init messages are the Noise prologue, so tampering with either fails
the handshake.

Message 1 can be read without the pre-shared key because the key is only mixed in at the end of
message 2. The flynn/noise library fixes the key when the state is created, so `respond` in
`secure.go` reads message 1 twice: once with a throwaway state to learn the `psk_id`, then again with
a state built on the chosen key, which is the one that writes message 2.

### Choosing the key

The room's candidates are every pairing record it holds, its own pairing key, and the published
Sentinel key. `trust.lookup` matches the named `psk_id` against them, bound to the server that sent
`server/init`. Three outcomes:

- A match. The connection runs on that key, and its category (long-term, pairing, or sentinel)
  decides what the server may do on it.
- A miss. In the initial handshake the room completes on the Sentinel anyway. The server's
  verification of message 2 fails against the key it meant and succeeds against the Sentinel, which
  tells it the room has lost the record and it can offer re-pairing. In a re-handshake a miss fails.
- A misbinding. The key matched a record held for a different server. That is not a miss but an
  attempt to use someone else's credential, and the handshake fails outright.

## Framing

After the handshake every frame is a Noise ciphertext whose plaintext starts with a message type
byte: `0` for JSON, `4` for a player audio chunk. Nothing the room sends is large enough to fragment,
so it never does. It reads both fragment encodings: the spec's type `1` with a flags byte, and the
earlier type `2` and `3` pair that Music Assistant's library still uses.

Audio chunks carry an 8-byte big-endian server timestamp before the codec payload. The current spec
adds a 4-byte `send_ahead` field after the timestamp, but no server sends it yet and there is no way
to tell the two layouts apart on the wire, so echod reads the 8-byte form that Music Assistant
produces. When aiosendspin moves, this needs to move with it.

## Establishment and activation

Once the channel is up, the server sends `server/hello`, the room answers `client/hello`, and the
server sends `server/activate`. Nothing else may flow before that first activate, so clock sync waits
for it and starts again after every re-handshake.

`client/hello` carries the room's name, device info, its trust level (`user` when it holds a record
for this server, `none` otherwise), the player role and its formats, the one pairing method it offers,
and whether it admits unpaired access.

`server/activate` says what the connection is for: a set of activities and the roles the room may
use. `admission.go` implements the spec's table of which sets each key category allows, and the three
rules for refusing one: `pairing_required` when only turning on unpaired access would have made it
admissible, `unauthorized` otherwise, and `pair/abort` with `method_not_supported` for a pairing the
room cannot do, which leaves the connection open.

Music Assistant declares a `management` activity on paired connections that the current spec no
longer lists. It is accepted and nothing is done with it.

## Unpaired access

A server the room has never paired with connects on the Sentinel key. Whether it may then play is the
`Sendspin unpaired access` switch in Home Assistant, on by default so a freshly installed Dot behaves
as it always did. Music Assistant gates unpaired playback on its own side too: the operator has to
approve the room there, or pair it.

Turning the switch off while an unpaired server is connected sends it `client/goodbye` with reason
`pairing_required` and hangs up.

## Pairing

The room offers the Pairing PSK method only. The operator copies the room's pairing token from Home
Assistant into the server. The token is the room's public key and its pairing key together, encoded
as the spec's `SP:0` string. The server then connects with the pairing key, the handshake matches it
under the pairing category, and the server activates `pairing` with method `pairing_psk`. The room
mints a fresh long-term key, sends it in `client/pair-finalize`, and on `server/pair-finalize` stores
it against the server's identity. The server then re-handshakes onto the new key inside the same
socket, and establishment runs again at trust level `user`.

The spec also defines two code-based methods where the operator types a code shown or spoken by the
device. They are not offered, deliberately. Their wire names changed between the version of
aiosendspin Music Assistant ships (`dynamic_pin`, `static_pin`) and the current spec
(`dynamic_pairing_code`, `static_pairing_code`), and a server rejects a `client/hello` that names a
method it does not know. Offering either name would break against one version or the other. The
token method has the same name and shape in both, so it is what the room offers until the servers
settle. The Dot has no display, so the natural code method later is the dynamic one shown in Home
Assistant.

### The trust file

`/data/misc/echolocal/sendspin.json`, mode 0600, written atomically and flushed the same way the
settings file is. It holds:

- `identity_key`: the Curve25519 private key. Its public half is the `client_id` every server knows
  the room by. Losing it makes the room a new device to every server.
- `pairing_psk`: the pairing key, generated once. Pairing does not consume it.
- `records`: one entry per paired server, with the long-term key and when it was last used. The
  room keeps up to eight; past that the one used longest ago is dropped, except the one being written
  and the one backing the open connection.
- `last_playback_server`: the server that most recently played, kept for the spec's tie-breaking
  between servers. Read but not yet acted on, because the listener admits one server at a time.

The identity is separate from the settings file because everything in it is a secret and none of it
is a preference. It is created on first use, by echod at start-up or by `echod tools sendspin`.

### Forgetting

`server/unpair` from a paired server drops that server's record and says goodbye with reason
`unpaired`. The `Sendspin forget pairings` button in Home Assistant drops every record; the identity
stays, so the room is still the same device to every server, just one they have to pair with again.

## Clock sync and state

Clock sync is unchanged in method: bursts of eight `client/time` probes every ten seconds, feeding the
best round to the filter. What changed is when it runs and what it gates. It starts after the first
activate and pauses across a re-handshake. The room does not report itself `available` until the
filter has converged, as the spec requires, and its `client/state` always carries the full player
object: volume, mute, the delay under both the spec's name and the one Music Assistant still reads,
the lead time and buffer the room asks for, and `set_static_delay` as the one command named there.
Volume and mute are said in the hello only: Music Assistant's library rejects a state that names them
and drops the connection, which is how the first install against a real Music Assistant 2.11 failed.

## Output delay

The room accepts the `set_static_delay` command (and the spec's newer name, `set_output_delay`).
Naming it in `client/state` is what makes Music Assistant show a per-player delay setting for the
room. The value is kept in the settings file, reported back in every state, and taken off each chunk's
timestamp so the room plays that much earlier. It is the knob for whatever constant offset remains
between rooms, set by ear from Music Assistant. Holding rooms together over the length of a track is
the renderer's drift correction, which is a separate change.

## Home Assistant entities

| Entity | Kind | Purpose |
| --- | --- | --- |
| Sendspin | switch | Whether the room listens at all. |
| Sendspin unpaired access | switch | Whether a server that has not paired may play. |
| Sendspin state | text sensor | off, waiting, joined, playing. |
| Sendspin security | text sensor | off, waiting, unpaired, pairing, paired. |
| Sendspin pairing token | text sensor | The token to enter into a server. |
| Sendspin forget pairings | button | Drops every pairing record. |

## Testing

`session_test.go` contains a small Sendspin server, the initiator side of the handshake plus framing
and a reader that answers clock sync, and drives the room through the flows: unpaired playback,
refusal when unpaired access is off, pairing by token with the in-band re-key and a return on the
record, an aborted pairing on the wrong key, the Sentinel fallback for a lost record, a misbound key,
an unencrypted server, `server/unpair`, and withdrawing unpaired access under a live connection.
`secure_test.go` pins the spec's Sentinel constants and exercises the handshake and both fragment
encodings. `trust_test.go` pins the spec's token vector and the eviction order. `admission_test.go` is
the activation table.

## Known gaps

- One server at a time. The spec has a room hold provisional connections and arbitrate by declared
  activity. A second server gets its socket closed instead.
- No code-based pairing, for the reason above.
- The 8-byte chunk header, for the reason above.
- The room does not report `available: false` when its speaker is taken by something else, such as a
  voice reply. It never did; the arbiter ducks and resumes instead.
