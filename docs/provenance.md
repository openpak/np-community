> Architecture correction, 2026-10-15: historical HLE/RPCN bridge, private
> user lookup, ticket export and bridge login work recorded below has been
> removed from active sources. Native firmware/real-console service parity is
> required. The single-process VSH replacement is offline-diagnostic-only.
> Native account/profile endpoints and private trophy storage are retained.

# PS3 sign-in and social wire provenance

Sources are our PS3 firmware 4.93 decompiles and Pier lab captures under
`Openpak/scratch/pier/`. No third-party server implementation was used.

- `/nav/auth`, request fields and headers: `capture/capture.jsonl`, 2026-10-15.
  VSH `FUN_0022eda8` receives the ticket after checking HTTP status and headers.
  Error reason `14` for refused credentials follows the native wrong-password
  convention; the exact nav/auth error reason remains unconfirmed.
- Ticket version 4.0 and four-byte header: VSH `FUN_00230d74`, handler tables
  at `0x6f9b04`, `0x6f9b24`, `0x6f9b44`. Ordered type/size table at `0x684558`.
  Account data container `3000`, entitlement container `3010`, tail fields
  13/14 (tag 8, length 8/64): `FUN_00230734`, `FUN_0022fe1c`.
- Ticket times are Unix milliseconds: `FUN_001bab9c` converts them to RTC
  microseconds. `FUN_005d3e24` enforces the allowed range. Seconds failed in
  the lab with `80010603`; milliseconds allowed sign-in to proceed.
- Native account ID is the existing adapter SHA-256 derivation from core
  account ID. Tickets share this function with bindAccount; mismatches produced
  the firmware's “another user's sign-in ID” screen.
- The final `3012` extension is an **OpenPak addition**, signed with an
  owner-local Ed25519 key, binding the complete firmware envelope and JSON
  claims. This is not a Sony signature or a claim that games verify it. The
  firmware sends back issued tickets with additional device data; verification
  authenticates the issued prefix and ignores untrusted device extensions.
  Issuer, domain `un`, flags, empty entitlements and zero extra fields remain
  provisional; the unsigned equivalent reached XMB in the lab.
- `/basic_view/sec/get_self_profile`, `/basic_update/sec/update_ptl` and
  `/basic_update/sec/update_ci`: captured 2026-10-15. Profile success attribute
  is `result="00"` (`FUN_00226644`). Required profile fields: `FUN_00213364`
  and its helpers `FUN_00224f24`, `FUN_002263a4`, `FUN_00224e48`,
  `FUN_00224c34`, `FUN_00224998`, `FUN_00226238`. Empty optional text passed.
  `FUN_001f8d98` validates JID domain structure.
- TLS port 5223: observed `SceNpCoreMain` socket connect. No listener gave
  `8002AD3D` (errno `3d`, ECONNREFUSED, combined with `8002ad00`).
- Stream header and `I5R-TICKET-AUTH`: `FUN_001cccc0`, `FUN_001cd2dc`;
  SASL success/restart `FUN_001ccd3c`; resource bind/session and roster were
  captured in `xmpp-lab.jsonl`. The lab reached signed-in XMB after these.
- Standard roster query and item/group parsing: `FUN_001bf178`. Include a
  group because the firmware sizes its roster storage by group count. The
  `both`/`none` subscription projection is based on standard XMPP semantics;
  actual populated PS3 roster display must still be verified. Profile presence
  extension `http://playstation.net/np/rt/profile`, child `name`: `FUN_001c0a5c`.
- Core `Social` and `Sessions` protos define canonical relationships, blocks,
  live leases and cross-platform presence. The adapter owns no friend graph.
  Every active OpenPak user is provisioned an `np` link, including users who
  have never signed in on PS3. Existing IDs are preserved; collisions receive
  deterministic account-derived suffixes. This provisioning policy is the
  user's explicit requirement, not an inferred Sony behavior.

Not implemented: Sony ticket signature verification in games, invitations,
private messages, offline-message storage, privacy-list editing, title-specific
NP presence payloads, or cross-process stream delivery. Unknown IQ operations
return service-unavailable. Core graph/presence polling runs every ten seconds;
blocking, removal, bans and link revocation are rechecked without reconnecting.

Lab trust configuration lives outside the server: VSH's CA-file pin is patched
for the installed OpenPak CA and its NP root-name pin changed to
`OpenPak Dev Root1`. TLS still validates chains; RSA-kex termination is required.

## Idle stream and avatars (2026-10-15)

The first authenticated stream signed itself out after about 122 seconds of
server silence. `stream-drop.pcap` shows client newline keepalives followed by
client unavailable presence, stream close and FIN; the server did not close
first. VSH FUN_001ce200 sets a 40-second send timer and sends keepalive traffic
through FUN_001cdd00. The adapter now sends a whitespace heartbeat every twenty
seconds; a regression test verifies repeated traffic on an otherwise idle stream.
The next live stream passed the old cutoff with bidirectional newlines captured.

Captured public `/basic_view/func/get_profile` requests contain a JID and avatar
size, **without a ticket**. Firmware normalizes those JIDs to
`online@cc.np.np.playstation.net`. The public handler now resolves the active NP
link and returns only public name/country/avatar data. Self profile still requires
a verified ticket. Blocking continues to govern roster/presence, not anonymous
public-profile lookup.

Avatar source is core `Media.GetAvatar(size=256)`, which validates the uploaded
PNG at write time. Profile `avatarurl` is limited to 127 bytes by FUN_00226238;
our real-account URL is 124 bytes. It contains a short, expiring, purpose-separated
128-bit HMAC capability derived from the owner-local ticket key. The image route
rechecks active status and serves the core PNG with ETag/conditional GET. No
avatar is invented for an account without an upload. The lab HTTP stub proxies
public profile and avatar requests to the adapter (firmware uses HTTP here).
AndroidTest's actual 1484-byte, 256×256 PNG was fetched through that live route;
XMB rendering remains to be verified after a profile refresh.
# Trophy discovery resource framing (2026-10-15)

After profile/avatar HTTP 200, XMB reported 8002A10B while fetching trophy-status.
VSH FUN_0022cd90 reads exactly 65 bytes matching
`<!--Sce-Np-Signature: ` + 40 hex digits + `-->`, then LF (or CRLF).
It subtracts the 66/67-byte frame before XML parsing. FUN_0022c698 computes
HMAC-SHA1 over XML zero-padded to a multiple of 64 bytes; type 5 selects the
20-byte owner-local compatibility key (firmware bytes XOR 0x7f). No firmware key
is committed. NP_TROPHY_RESOURCE_KEY supplies a local raw 20-byte key file.
Trophy discovery reports maintenance, since trophy operations are not implemented.
Wire framing has an independently computed test vector; actual XMB behavior after
this correction still requires a retry.


# RPCN trophy facade and native reads/sync (2026-10-15)

> Superseded 2026-10-16: np-trophy (openpak/np-trophy, Go, Postgres) serves the same
> contract; the openpak/rpcn fork and its local checkout were deleted. The facts below
> are history.

Backend base: RipleyTom/rpcn commit 253b3fe79baa35fad11f1ec9d9886837f02b89b4,
AGPL-3.0. Fork https://github.com/openpak/rpcn, draft PR #1. RPCN storage and bulk
merge are reused on the server only; no AGPL code is copied into GPL-2.0 Pier.
The facade keys disabled storage-only rows by immutable core account UUID, keeping
OpenPak core as the identity authority.

Owner-local firmware 4.93 np_trophy_util.sprx was decrypted in a separate config
profile without stopping Pier, then analyzed in Ghidra. Analysis artifacts remain
under scratch/pier/trophy-work, not committed. Wire facts from FUN_00046070 and
00045e70: POST /trophy/sec/sync_trophies carries nptrophy/ticket and list/info with
npcommid, nested trophies/trophy attributes id and type, and a timestamp as text.
IDs are one-based on this wire; grades 0/1/2/3 mean bronze/silver/gold/platinum.
The response parser FUN_0004abf8 accepts nptrophy result hexadecimal 00, list/info,
point, level(base,next), types(platinum,gold,silver,bronze), and trophy entries.
Summary endpoint is /trophy/sec/get_multi_user_info_privacy; title list endpoint
is /trophy/sec/get_title_list_privacy. Native live validation remains required.
VSH FUN_0021920c requires alive discovery to include a trophy-fqdn list. It is
advertised only while the private backend returns a successful persistent read.

Legacy PS3 point/level scale facts are documented in
https://www.psdevwiki.com/ps3/Trophy (15/30/90/180 points by grade; thresholds
0,200,600,1200,2400,4000,6000,8000,10000,12000,14000,16000, then +8000).
Unknown stored grades fail rather than fabricate counts. Native catalog assets,
privacy writes and full nonempty title rendering remain unimplemented; the private
backend supports persisted unlocks independently. No gameplay proof is asserted.

## Native CAM account-management projection (2026-10-15)

Observed: authenticated /native/cam/authenticate.action reaches the core but
the previous identity-only response leaves UI error80023087 (user once typed
80033087). Firmware4.93 regcam FUN_0006b1e0 parses the reply through
FUN_000cdd70 -> FUN_000dad38, then requires identity5e throughFUN_000dd9ac
and raw settings5f throughFUN_000d8f58 (minimum0x3c bytes, fifteen BE32
fields). It then unconditionally fetches service list16 index0 through
FUN_000cdd08 -> FUN_000da04c. That item has0x1e fixed bytes, minimum0x2a
bytes overall, required nested list15 and required raw strings7d/7e.
Top-level optional lists64 and58 drive further loops; absent lists give0.
These are independently implemented wire-layout facts from local firmware
analysis; no firmware implementation code is copied.

CAMReply now adds a zero settings projection and a single service entry with
zero fixed fields, empty option list15 and empty7d/7e. Individual fixed-field
semantics are not established; zero is an explicit lab default, not verified
Sony account settings. No wallet, currency, billing address or payment details
are invented. This experiment still requires actual UI acceptance testing.
Wrong passwords and disabled-account checks still use the real core.
Native handler regression independently walks the fields, rejects the old
bind-only response and malformed/truncated bodies. Native/oauth/server tests
passed. No claim that every Account Management submenu is implemented.

## Authenticated Pier HLE session bridge (2026-10-15)

The single-process XMB-to-game handoff destroys VSH's in-memory NP session;
upstream HLE game services otherwise report PSN disconnected. An opt-in private
bridge exports the signed ticket from the successful VSH `/nav/auth` response
into an atomically replaced owner-only file (`NP_PIER_SESSION_FILE`). It uses
JSON/YAML cfg_rpcn fields and the explicit loopback `NP_PIER_RPCN_HOST`, never a
core password. Other native service tickets do not replace this session.

`POST /internal/pier/verify` requires a loopback caller and the independent
`NP_PIER_BACKEND_KEY`. It validates the supplied ticket with the normal signature,
expiry, account status and canonical-link checks, additionally binds online ID
and the VSH service, then projects core identity/country and current allowed
friends. It is not an unauthenticated status toggle or a new account authority.
The RPCN fork consumes this projection in its isolated local matching2 mode.

The lab runner loads owner-local online-service/service.env, clears stale ticket
transport on a fresh lab launch, and supervises the matching backend alongside
the existing adapter. Pier's `PIER_OPENPAK_SESSION` opts games into this transport;
VSH continues native authentication. Missing transport stays offline and the
backend rejects expired or revoked credentials. Normal RPCN settings remain the
default when the bridge is not opted in. Multiplayer gameplay is a separate gate.

Pier HLE avatar projection follow-up: actual game login reached backend but
client rejected empty avatar_url (rpcn_client::login uses get_string(false)).
The bridge omitted existing core avatars. pierVerify now exports core-backed
AvatarURL for self and friends, reporting503 if unavailable. RPCN projection
stores and refreshes those URLs instead of empty strings. No placeholder or
client validation relaxation is used. Adapter regression fetches the returned
URL and checks the core PNG; live preflight fetched PNGs for user+3friends.
Full backend login-wire regression covers avatar and friendship/presence
fields plus avatar update on reconnection. Game-level validation pending.

## Native Matching2 discovery (2026-10-15)

Firmware VSH constructs /np/resource/title/<communication-id>/matching/
<communication-id>-matching.xml (operation0x1c in URL builder) and consumes a
signed matching2 XML root with status/titleid followed by agent-fqdn entries
containing id/status/port and hostname text. Added the route to the existing
community adapter with signed maintenance while no native Matching2 agent is
available. No RPCN listener is advertised. Tests independently verify MAC and
XML/path/error behavior. This is an observed native resource layout; no Sony
firmware code is copied. Agent protocol and live firmware acceptance remain
unverified. This supersedes the HLE bridge as the intended production direction.
