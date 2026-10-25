# Notes: np-community

OpenPak's independent PlayStation 3 community service: profiles, public avatar
capabilities, canonical core friends and leased XMPP presence. No local identity
or relationship database. Native sign-in and ticket issuance remain in np-account;
profile and XMPP authentication verify those tickets and current core standing.

Operator tools moved here: `cmd/np-identities` (list, migrate, inspect roster) and
`cmd/np-avatar-backfill` (fill missing core avatars from the website).
See [migration](https://github.com/openpak/np-account/blob/main/docs/service-split.md).
