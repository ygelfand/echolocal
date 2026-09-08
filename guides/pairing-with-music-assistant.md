# Pairing an Echo Dot with Music Assistant

From this release the Dot talks to Music Assistant over an encrypted connection, and you can pair the
two so Music Assistant knows it is really your speaker. This guide walks through what you will see
and what to do.

## What changed

Music Assistant used to show the Dot with the warning "This device is connected without encryption
(legacy mode). Pairing is not available." That warning is gone. The connection is encrypted from the
first frame, and Music Assistant shows the Dot in one of two states:

- **Connected without pairing.** Music Assistant can use the speaker, but nothing has proven that the
  device on the other end is your Dot rather than something else on the network claiming its name.
- **Paired.** The Dot and Music Assistant hold a shared key. Each knows the other, and nothing else
  can pose as either.

Unpaired playback is fine on a home network you trust. Pairing is a one-time step that takes about a
minute, and it is what Music Assistant will expect as the default over time.

## Before you start

You need:

- A Dot running this version of EchoLocal, added to Home Assistant.
- Music Assistant 2.10 or newer with the Sendspin provider enabled, which it is by default.

If you had the Dot in Music Assistant before this update, it will appear as a new player. The Dot now
identifies itself by a cryptographic key rather than its MAC address, so Music Assistant cannot tell
it is the same device. Remove the old entry once the new one is working.

## Letting it play without pairing

Nothing to do on the Dot. Music Assistant discovers it, and the first time you try to play to it
Music Assistant asks you to approve the device, or you can approve it from the player's settings. In
Home Assistant the Dot's `Sendspin security` sensor reads `unpaired` while a server is connected this
way.

If you would rather the Dot refuse any server that has not paired, turn off the `Sendspin unpaired
access` switch on the Dot's device page in Home Assistant. A server that is connected without pairing
is disconnected at that moment and told to pair first.

## Pairing

1. In Home Assistant, open the Dot's device page and find the `Sendspin pairing token` sensor under
   Diagnostic. It is a string starting with `SP:0`. Copy it.

   If you prefer the command line, run this on a computer with the Dot connected over USB:

   ```sh
   echoctl tools sendspin
   ```

   It prints the same token.

2. In Music Assistant, open Settings, then Players, and pick the Dot. Choose Pair. Music Assistant
   asks for a PIN or a pairing token. Paste the token.

3. Music Assistant connects to the Dot with that token, the two exchange a long-term key, and the
   player's status changes to Paired. In Home Assistant the `Sendspin security` sensor changes to
   `paired`. This takes a second or two.

That is it. The token stays valid, so you can use the same one to pair the Dot with another Music
Assistant server later, or to pair again if either side forgets the other. Treat it like a Wi-Fi
password: anyone holding it can pair a server with your Dot.

## Unpairing

To remove the pairing from the Music Assistant side, unpair the player in its settings. Music
Assistant tells the Dot, which forgets the key and disconnects. Playback then needs either unpaired
access to be on, or a new pairing.

To remove every pairing from the Dot's side, press the `Sendspin forget pairings` button on its device
page in Home Assistant. The Dot keeps its identity and token, so servers still recognise it, but each
has to pair again before it is trusted. The next time a paired server connects it will notice the Dot
no longer holds its key and offer to re-pair.

## If something is off

**Music Assistant does not see the Dot.** Check the `Sendspin` switch is on and the `Sendspin state`
sensor reads `waiting` or `joined`. The Dot advertises itself on the local network; Music Assistant
has to be on the same one.

**The Dot shows as a new player after updating.** Expected, see above. Remove the old entry.

**Pairing fails straight away.** Copy the token again, whole. It is long, and one character wrong
means Music Assistant is trying to reach a device that does not exist. Also check the Dot is not
already connected to a different Music Assistant server: it talks to one at a time.

**The Dot says `unpaired` but Music Assistant says it is paired.** Music Assistant is probably
holding a key the Dot no longer has, for example after `Sendspin forget pairings`. Unpair and pair
again from the Music Assistant side.

**Music Assistant is on an old version.** A Music Assistant that still speaks the unencrypted
protocol cannot connect to the Dot at all any more. Update it.

**The pairing token sensor is empty.** The Dot could not read or create its credentials file. Look
at the logs with `echoctl logs` for a line starting `sendspin credentials`.
