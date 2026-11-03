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
| `/np/resource/title/{title}/matching/{resource}` | Matching2 discovery (maintenance unless `NP_MATCHING_AGENT=host:port` is set) |
| `/healthz` | Health check |
| XMPP, `NP_XMPP_ADDR` (default `:8523`) | Core friends roster and leased presence |

## Run

```sh
# Export values from example.env after supplying operator credentials.
GOWORK=off go run -mod=vendor ./cmd/np-community
```

## Docs

- [Protocol](docs/protocol.md)
- [Deploy](docs/deploy.md)
- [Wire provenance](docs/provenance.md)

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).

## Matching2 recorder

The image also carries `matching2-recorder` (`cmd/matching2-recorder`), a capture-only listener for
the PS3 Matching2 agent connection: plain TCP plus RSA-kex TLS (leaf minted from the PS3 CA for
`M2_HOST`). It never replies and writes a hexdump per connection to `M2_LOG_DIR`. Env: `M2_HOST`
(required), `M2_CA_CERT`/`M2_CA_KEY` (default `/keys/ps3-ca.{pem,key}`), `M2_PLAIN_ADDR` (`:3480`),
`M2_TLS_ADDR` (`:8443`), `M2_LOG_DIR` (`/captures`). Run it with the image entrypoint overridden.
