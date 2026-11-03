# Changelog

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
