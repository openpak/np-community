# Protocol: np-community

Routes: `/basic_view/sec/get_self_profile`, `/basic_view/func/get_profile`,
`/basic_update/sec/update_ptl`, `/basic_update/sec/update_ci`, `/a/{cap}`,
`/np/resource/title/{title}/matching/{resource}`, `/healthz`.
No native login or trophy routes are registered here.

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

`<message>` stanzas (text, game invitations and game-data attachments up to the 32 KiB stanza budget) are routed between
accepted friends only. The stream identity sets `from` (client values are discarded, as is any client-sent
`http://playstation.net/np/rt/stored` marker). The recipient's open streams get the stanza at once; with none open it is
stored (Postgres table `np_messages`, 100 per recipient, 30 days) and replayed after the recipient's
`<iq><fetch xmlns="http://playstation.net/np/rt/stored"/></iq>` with `<x xmlns=".../stored" id stamp expire delay="1"/>`,
then deleted. Errors come back as `<message type="error">`: `not-acceptable` (not a friend), `policy-violation` (too large),
and `resource-constraint` with `<x xmlns=".../max_exceed"/>` for a full mailbox. Wire shapes are static RE only; see the
private research note `ps3/messaging-protocol.md`. The `uploader` attachment host is not served here.

## Message settings

`POST /friend_list_update/sec/set_message_setting` and `/friend_list_view/sec/get_message_setting`
(hosts `friend-list-update.ww.<env>.communication.playstation.net` and `friend-list-view.<cc>.<env>.communication.playstation.net`).
Request `<message-setting platform sv env><ticket/><setting type="N">V</setting>...</message-setting>` (get omits the text);
reply `<message-setting result="00"><setting type="N">V</setting>...</message-setting>`, with only the types that have a stored
value (the firmware keeps -1 for the rest). Stored per account as opaque integers in Postgres `np_message_settings`
(memory without `NP_DATABASE_URL`). The meaning of the type numbers is not in the firmware, so they do not change routing:
messages go to accepted friends only. Static RE only; see the private research note.

## Scope

Profiles, public avatar capabilities, canonical core friends, leased XMPP presence and the offline message queue (the only local state). No local identity or relationship database; profile and XMPP authentication verify np-account tickets and current core standing.
