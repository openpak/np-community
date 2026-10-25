# PS3 Community Server

OpenPak's PS3 Community Server.

| | |
| --- | --- |
| Network | PS3 |
| Role | Profiles, public avatar capabilities, canonical core friends and leased XMPP presence |
| Depends on | [`np-account`](https://github.com/openpak/np-account) (sign-in and ticket issuance), OpenPak core account service (`NP_CORE_ADDR`) |
| Image | `ghcr.io/openpak/np-community` |
| Licence | AGPL-3.0-only |

## What it serves

| Endpoint | What |
| --- | --- |
| `/basic_view/sec/get_self_profile`, `/basic_view/func/get_profile`, `/basic_update/sec/update_ptl`, `/basic_update/sec/update_ci` | PS3 profile read and update |
| `/a/{cap}` | Public avatar capability route |
| `/np/resource/title/{title}/matching/{resource}` | Matching2 discovery (advertises maintenance) |
| `/healthz` | Health check |
| XMPP, `NP_XMPP_ADDR` (default `:8523`) | Core friends roster and leased presence |

## Run

```sh
# Export values from example.env after supplying operator credentials.
GOWORK=off go run -mod=vendor ./cmd/np-community
```

## Docs

- [Protocol](docs/protocol.md)
- [Evidence](docs/evidence.md)
- [Deploy](docs/deploy.md)
- [Notes](docs/notes.md)
- [Wire provenance](docs/provenance.md)

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).
