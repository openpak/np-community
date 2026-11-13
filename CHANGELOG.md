# Changelog

## 0.4.13 - 2026-11-13

- Signalling toward a bridged host is held, not bounced (prds#177). A PS3 that joins a Wii U-hosted game signals to the NpId in the owner's
  member entry (`WiiUTest` + `gbgbps3`), which the firmware addresses as `WiiUTest@gb.gb.np.playstation.net/ps3`. The relay already
  recognised the host by its online id and kept the stanza for the directory (`GET /internal/bridge/signaling`), but then fell through to the
  generic handler, which answers an unknown iq with `service-unavailable`; the firmware reads that as the peer being unreachable and the
  joiner leaves the room. A stanza toward a live bridged host is now handled (kept, logged in full, no reply), so the joiner retries until the
  directory's transport leg answers. A stanza toward anybody else falls through as before. Test: `TestSignalingToABridgedHostIsHeldNotBounced`
  (the community JID with and without resource, and the `<cc>.<cc>.np.playstation.net/ps3` form the PS3 uses, `set` and `result`).

## 0.4.12 - 2026-11-11

- Bridged presence is the real thing (prds#177): the first real-console test showed PS3 friends the host's ordinary "Online on Wii U" only. The
  presence a bridged host gets is now built in the form of the stanza the owner's real PS3 sent while hosting Minecraft (2026-11-11
  mc1.log): `stanzatype`, an `ext` element (`context`/`origin` = the communication id, the 56-byte blob as `data inline="base64"`) and the
  `presence` element (`mode` game, `objectId` NPEB01899_00, `localName`, `localLang` 18, `gameSpecified`); a test feeds that real stanza through the relay's
  own parser and requires the bridged children to be identical. PUT also takes optional `product`, `name` and `text`. Guessed: the object id of other
  regions and the `gameSpecified` text with several players.
- Selection: the bridged presence is shown unless the account's own console presence carries game data (an `ext` element, i.e. it is
  hosting itself); the plain sign-in presence a console stores (mode=game, no data) no longer hides it.
- A PUT with new content and a DELETE refresh every signed-in stream at once (no waiting for the 10 s poll); a viewer who signs in later gets it
  with the roster as before. Tests: an online viewer receives it on the PUT with the poll at 30 s, and loses it on the DELETE.
- Logging: every presence a console sets is logged in full (`NP presence xml`: all children, plus `blobs_hex` with each base64 data blob as hex), not
  once per account, so the next real hosting session gives the exact stanza.

## 0.4.11 - 2026-11-11

- Bridge routes for Minecraft cross-play (prds#177, common/minecraft docs/session-mapping.md), bearer `NP_BRIDGE_KEY` (off without it):
  `PUT/DELETE /internal/bridge/presence/{account}` keep the game presence (the 56-byte blob, or raw XML children) of a player hosting on another
  console, shown to the friends who see the account online, in memory with a 90 s lease the directory renews; a real console's own stored presence
  data wins while it has any. `GET /internal/bridge/signaling?after=N` returns the NP signaling stanzas addressed to a bridged host's JID, which no
  console holds, in full (also logged as `NP signaling toward a bridged host`; the stanza is handled as before). The element that wraps the blob is a
  guess until a real console's presence stanza is seen: the first presence data of each account per process is now logged in full (`NP presence xml`).

## 0.4.10 - 2026-11-10

- Game presence data reaches friends (prds#177, prds#163, Minecraft: PlayStation 3 Edition hosting). The relay showed a friend only the name and a status line synthesized from the core's presence, so a friend could see someone online but never the world: the game's presence data (Minecraft 01.84: a 56-byte room blob, room id, server id, net version) travels in the console's own `<presence>` children and was dropped. An available presence now stores its children (except the console's own `profile` `<x>` and `<status>`, which the relay writes itself; capped at 4 KiB) in Postgres `np_presence_data` (new table, kept like message attachments: the core's presence has no field for it), and each online friend's presence is sent with those children appended, resent when they change. Removed on unavailable, disconnect and account erasure; rows older than two days are purged at start. The stanza layout of a real console is not captured yet, so the children are relayed verbatim; `NP presence set` logs their element names and byte count. Needs `NP_DATABASE_URL` (already required).
- Discovery for NPWR05636_00 is configuration: add it to `NP_MATCHING_AGENT_TITLES` (a test pins the answer).

## 0.4.9 - 2026-11-10

- RT stream: bare `<iq type="result"/>` and `type="error"` stanzas (no netinfo/udpp2p `<query>` child) addressed to another connected console are now forwarded too. Real PS3 firmware acks udpp2p offers with bare results and only starts NP signaling probes after the ack of its own offer, so PS3-to-PS3 signaling never worked through 0.4.8 (prds#145). Results/errors that match no connected console still fall through to the server handlers.

## 0.4.8 - 2026-11-07

- RT stream: NP signaling stanzas (`http://playstation.net/np/rt/netinfo`, `http://playstation.net/np/rt/udpp2p`, any iq type including result/error) addressed to another connected console are forwarded to it with the sender's JID in `from=`. Before, peer signaling got `service-unavailable`, so `sceNpSignaling` activation between consoles could never start (Terraria cross-play relay, prds#145).

## 0.4.7 - 2026-11-05

- `NP_MATCHING_AGENT_TITLES` (comma-separated communication ids): the Matching2 discovery resource advertises the agent only for those titles, others stay in maintenance. Empty keeps the old behaviour (all titles, for the capture recorder).

## 0.4.6 - 2026-11-05

- `matching2-recorder`: session key derived from the title passphrase instead of random, so a restart does not strand a connected console.

## 0.4.5 - 2026-11-05

- `matching2-recorder`: login reply `0x74` = 600 (session lifetime in seconds; vsh renews the token at 80%, and 1 made it renew
  continuously), `0x14` = 12345.

## 0.4.4 - 2026-11-05

- `matching2-recorder`: session key shared between the TLS login and the plain connection (per communication id); answers `0x1003`
  (token renew) and `0x1008` (world list, one world) with session-key MACs.

## 0.4.3 - 2026-11-05

- `matching2-recorder`: answers the `0x1002` login (session key `0x22`, tokens `0x23`/`0x5d`, `0x14`, `0x74`; values are ours, the
  shapes come from the firmware's reply parser) and MAC-checks later requests with that key; ticket, passphrase and session key
  values are redacted from the capture files.

## 0.4.2 - 2026-11-05

- `matching2-recorder` answers `0x1001` (context start) for titles listed in `M2_PASSPHRASES` (`COMMID=<256 hex>`), after
  verifying the request MAC (HMAC-SHA1 keyed by the first 16 bytes of the title passphrase, checked against a real Terraria
  request captured in Pier 2026-11-05). Without an entry it stays capture-only. Reply content of `0x1d` is a guess; see
  research/ps3/matching2-protocol.md.

## 0.4.1 - 2026-11-05

- Matching2 discovery resource is signed with its own firmware key (`NP_MATCHING_RESOURCE_KEY`, 20 bytes, vsh verify mode 3 =
  `0x684048 ^ 0x7f`); before, the trophy key signed it and vsh would reject it as a bad signature. Falls back to `NP_TROPHY_RESOURCE_KEY`.

## 0.4.0 - 2026-11-04

- Account erasure (card #120): `DELETE /internal/accounts/{account_id}/data` (bearer `ACCOUNT_ERASE_KEY`, listener `ERASE_ADDR`,
  default `:9099`; not served when the key is unset) removes the account's `np_chat_cursor`, `np_message_settings` and
  `np_message_ext` rows (sent or received) and logs one `account erased` line. Idempotent; other accounts untouched.
  Also runs on the core `account_deleted` event (np-common `events.Consumer.Purge`).
- `np_message_ext` gains `sender` and `recipient` account columns (added on start) so a message extension can be matched to
  the accounts involved. Older rows have no owner and expire with the existing 35-day retention.
- Decision per table: cursor, settings, extensions are deleted (no authored content others keep here; message text is in chat).

## 0.3.5 - 2026-11-03

- Every request logs one stderr line, `http <method> <path> -> <status> <duration>ms` (np-common v0.1.2 `httplog`,
  through `nphttp.Logging`): no query, headers, credentials or bodies; `/healthz` skipped; the PS3 status header is
  appended as `ps3_status=<n>` when set. Replaces the slog "NP HTTP" line, which had no duration.

## 0.3.4 - 2026-11-03

- `NP_DATABASE_URL` is now required: the service exits at start without it instead of falling back to in-memory cursors and settings. Pool capped at 3 connections.

## 0.3.3

- Friend messages now go through the core-neutral chat service (`NP_CHAT_ADDR`, `NP_CHAT_KEY`): a PS3 message reaches friends
  on every OpenPak network and the reverse. Outbound stanzas become `chat.SendMessage` (text; subject and body joined with a newline).
- Inbound: follows the core event stream for `type=message`, delivers to open streams, and replays pending messages with the
  stored marker on the stored-fetch iq, using a per-account cursor (`np_chat_cursor`).
- PS3-only attachments (`np/rt/content`, `message_kind`) and the original subject/body split are kept in `np_message_ext`.
- BREAKING: the `np_messages` offline queue is removed and its table dropped on start. Without `NP_CHAT_ADDR` messages answer
  `service-unavailable`; the `max_exceed` mailbox-full error no longer exists.
- Event logs for sign-in, roster, presence and message in/out (account and counts, no bodies).
