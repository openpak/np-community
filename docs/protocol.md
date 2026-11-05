# Protocol: np-community

Routes: `/basic_view/sec/get_self_profile`, `/basic_view/func/get_profile`,
`/basic_update/sec/update_ptl`, `/basic_update/sec/update_ci`, `/a/{cap}`,
`/np/resource/title/{title}/matching/{resource}`, `/healthz`.
No native login or trophy routes are registered here.

## Matching2 discovery

`GET /np/resource/title/{commid}/matching/{commid}-matching.xml` returns a signed resource.
Default: `<matching2 status="maintenance" titleid="..."/>`. With `NP_MATCHING_AGENT=host:port` it is
`<matching2 status="alive" titleid="..."><agent-fqdn id="1" status="alive" port="PORT">host</agent-fqdn></matching2>`;
the console then opens plain TCP to `host:PORT` and TLS to `host:443`. No agent implementation
exists; point it only at `matching2-recorder`. Signed with `NP_MATCHING_RESOURCE_KEY` (vsh mode 3), not the trophy key.

## NSX feeds

`GET`/`HEAD https://nsx.sec.<env>.dl.playstation.net/nsx/sec/Xz78TMQ1Uf31VCYr/c/NSXWSV/NSXWSV-PN.P3.[GAME.|VIDEO.]<CC>-<FEED>.xml`,
read by `explore_category_psn` (firmware 4.93). The XMB requests the Game and Video column
`BILLBOARD0000001`, `XMB_TV_INSTALL01` and `XMB_COLUMN_VIDEO` at every sign-in; What's New reads
`WHATSNEW00000001`. Served from the embedded `community/nsx` tree; the country is dropped from the
name (`GAME-BILLBOARD.xml`), so items must not depend on it.

Shape, from the parser (attribute vs child as the firmware reads them):

```xml
<nsx anno="max-age=3600">            <!-- root: optional min-sys-ver; anno max-age = cache seconds, default 86400 -->
 <spc>                               <!-- or sub-spc -->
  <mtrl id="..." from="" until="" lastm="" anno="new=1&picks=1&emergency=0&rtype=0">
   <name/> <owner/> <desc/>          <!-- child text, 40/40/80 bytes -->
   <lang>all</lang>                  <!-- optional, repeatable: CELL_SYSUTIL_LANG number or "all" -->
   <cntry agelmt="0" uagelmt="99">pt</cntry>  <!-- optional, repeatable: country or "all" -->
   <url>https://.../x.png</url>      <!-- png/jpg/jpeg/gif image, swf; up to 2048 bytes -->
   <data type="120">base64</data>    <!-- optional, 1024 bytes decoded -->
   <target type="p|c|u">...</target> <!-- product id, store category, or http(s) URL (mp4 on .dl.playstation.net plays) -->
  </mtrl>
 </spc>
</nsx>
```

The root element name and the `from`/`until`/`lastm` date format are not confirmed; a value
the firmware cannot parse leaves the item always valid.

## Profiles, roster and XMPP

- Every active OpenPak user gets a real `np` link even without a PS3 login. A background reconciler scans the core's
  home-only `Identity.ListAccounts` directory at startup and every minute; existing IDs never change and a display-name
  collision gets a stable account-derived suffix. Friend-roster reads provision missing links immediately.
- The roster comes from core `Social.ListFriends`, filtered through `CheckInteraction` and active-account checks. Pending
  requests use the core's request/accept lifecycle. Presence registers a core session only after available presence,
  renews its lease and expires on unavailable or disconnect. Graph and presence changes are projected every ten seconds;
  removals and blocks produce roster removal and unavailable presence.
- Stanza input, nesting, deadlines, writes and connection count are bounded; unsupported messages and IQ operations
  fail explicitly. The stream sends whitespace keepalives every twenty seconds (TCP ACKs alone do not keep VSH's
  receive-idle timer alive) and incoming whitespace refreshes the idle deadline.
- Avatar URLs come from the core's validated 256px PNG; short signed capabilities fit VSH's URL buffer. HTTP
  `/basic_view/func/get_profile` and `/a/` are served as well as HTTPS self-profile. Public friend-profile requests carry
  no ticket and return public profile data only; account status is checked on profile and image reads.
- Firmware can serialize friend JIDs with the viewer's region, so the PlayStation domain shape is validated independently
  of the target account country.
- `cmd/np-avatar-backfill` copies the website's deterministic default PNGs (both sizes) into the core for accounts with
  no avatar; existing avatars are skipped.
- Online friend status uses the core session namespace for the console name and the reported client for known emulator
  names, such as `Online on Switch (Harbor)` or `Online on PS3 (Pier)`. Hardware and unknown clients omit the
  parentheses. Reported title IDs follow the platform label when present.

## Friend messages

Friend messages ride the core-neutral chat service (`NP_CHAT_ADDR`, gRPC `openpak.chat.v1.Chat`, key `NP_CHAT_KEY`), so a
PS3 message reaches the friend on whichever OpenPak network they use, and the reverse. np-community keeps no message queue;
chat is the store.

**Outbound** (`<message>` from a PS3 to a roster friend; `from` and any client `stored` marker are discarded):

- `chat.SendMessage(from = sender account, to = recipient account, kind = text, body, dedup_key)`.
- `body` = `subject` + `\n` + `body` (only the one present; `[attachment]` when the stanza has attachments but no text; an
  empty stanza is `bad-request`). Chat caps bodies at 64 KiB; the stanza budget stays 24 KiB.
- `dedup_key` = stanza id + recipient + hash of subject/body/attachments, so a repeated stanza is stored once.
- Attachments (`np/rt/content`, `np/rt/message message_kind`, ...) cannot ride chat. The original subject/body split,
  stanza id/type and the other child elements are stored in Postgres `np_message_ext` (keyed by chat message id, pruned after 35
  days) and handed back only to PS3 recipients. Friends on other networks get the text.
- Errors come back as `<message type="error">`: `not-acceptable` (not a friend, per the roster or chat), `policy-violation`
  (too large or rejected by chat), `bad-request`, `service-unavailable` (chat not configured), and `wait` +
  `internal-server-error` when chat is unreachable (the stanza is not retried or stored here).

**Inbound**: the service follows the core's event stream (the position is kept in `np_chat_follow`; the first start jumps to
the head so older history is not replayed). On `type=message` for an account it fetches the message with `GetMessage`. If
the account has a stream open the message is written to every open stream at once, otherwise nothing happens and it stays
pending. Per-account delivery state is `np_chat_cursor` (highest delivered chat id; an account gets its row at its first message
event, so pre-existing chat history is never pushed to a console).

**Replay**: on the recipient's `<iq><fetch xmlns="http://playstation.net/np/rt/stored"/></iq>` (and ahead of any live message)
everything after the cursor is sent oldest first with `<x xmlns=".../stored" id=<chat id> stamp=<sent_at> expire=<sent_at + 30 days> delay="1"/>`;
the cursor advances per message written. At most 100 (newest) are replayed; older and expired (30 days) ones are skipped.

Stanza shape of a message that did not come from a PS3: `from` = sender's JID/ps3, `id` = `chat-<chat id>`, `type=chat`,
`<subject>` = first line and `<body>` = the rest (body only when there is one line), plus
`<x xmlns="http://playstation.net/np/rt/message" message_kind="txt"/>` (the value is a guess; the firmware only limits it to 3
characters). Paintings, screenshots and app data arrive as a label (`[painting]`). Wire shapes are static RE only; see the
private research note `ps3/messaging-protocol.md`. The `uploader` attachment host is not served here.

Known limit: the cursor is a high-water mark, so a message committed by chat after a higher id was already delivered to the same
recipient (two sends in the same few milliseconds) is not replayed.

Logs (no bodies): `NP stream authenticated`, `NP roster sent`, `NP presence set`, `NP message out`, `NP message in`,
`NP message pending`, `NP stored fetch`, each with `account=<core account id>`.

## Message settings

`POST /friend_list_update/sec/set_message_setting` and `/friend_list_view/sec/get_message_setting`
(hosts `friend-list-update.ww.<env>.communication.playstation.net` and `friend-list-view.<cc>.<env>.communication.playstation.net`).
Request `<message-setting platform sv env><ticket/><setting type="N">V</setting>...</message-setting>` (get omits the text);
reply `<message-setting result="00"><setting type="N">V</setting>...</message-setting>`, with only the types that have a stored
value (the firmware keeps -1 for the rest). Stored per account as opaque integers in Postgres `np_message_settings`
(memory without `NP_DATABASE_URL`). The meaning of the type numbers is not in the firmware, so they do not change routing:
messages go to accepted friends only. Static RE only; see the private research note.

## Scope

Profiles, public avatar capabilities, canonical core friends, leased XMPP presence and, in Postgres, the chat delivery cursors and PS3 message attachments (the only local state). No local identity or relationship database; profile and XMPP authentication verify np-account tickets and current core standing.
