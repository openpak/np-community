# Changelog

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
