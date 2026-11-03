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
| XMPP, `NP_XMPP_ADDR` (default `:8523`) | Core friends roster, leased presence and friend messages (via chat, `NP_CHAT_ADDR`) |

## Run

```sh
# Export values from example.env after supplying operator credentials.
GOWORK=off go run -mod=vendor ./cmd/np-community
```

## Friend messages

Messages go through the neutral chat service so they reach friends on every OpenPak network. Set `NP_CHAT_ADDR`
(`host:port`, gRPC) and `NP_CHAT_KEY` (chat's internal key); unset, messages answer `service-unavailable`. With
`NP_DATABASE_URL` the delivery cursors and attachments persist (tables `np_chat_cursor`, `np_chat_follow`,
`np_message_ext`; the old `np_messages` queue is dropped on start). The chat client is generated into
`internal/chatpb` from `openpak/chat`'s proto (`protoc --go_out --go-grpc_out`, package name unchanged).

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
