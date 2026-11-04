# Changelog

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
