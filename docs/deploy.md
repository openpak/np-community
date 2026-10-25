# Deploy: np-community

Required: `NP_CORE_KEY` (at least 32 bytes) and `NP_TICKET_KEY` (the existing shared
Ed25519 PKCS8 key). `NP_CORE_ADDR` defaults to `localhost:7000`.
HTTP defaults to `NP_HTTP_ADDR=:8003`, XMPP to `NP_XMPP_ADDR=:8523`.
This service does not need OAuth client credentials or `NP_TOKEN_KEY`.

`NP_SESSION_CLIENT=ps3` and optional `NP_SESSION_OS` label presence.
`NP_PROVISION_ACCOUNTS=false` disables periodic all-account link reconciliation;
roster reads still provision missing links. Existing links are never renamed.
Core events independently disconnect banned/deleted accounts' XMPP streams.

`NP_TLS_ADDR`, `NP_TLS_CA_CERT`, `NP_TLS_CA_KEY` enable HTTP TLS.
`NP_XMPP_TLS=1` enables firmware TLS on XMPP using the same CA, even without HTTP
TLS. Route external TCP 5223 here directly. With an external compatible TLS
terminator leave that flag unset and keep the plaintext listener private.
Optional `NP_TROPHY_RESOURCE_KEY` supplies the 20-byte firmware resource key for
Matching2 discovery, which continues to advertise maintenance.

```sh
# Export values from example.env after supplying operator credentials.
GOWORK=off go run -mod=vendor ./cmd/np-community
GOWORK=off go test -mod=vendor ./...
GOWORK=off go vet -mod=vendor ./...
podman build -t np-community .
```

Shared np-common code is vendored;
refresh with sibling np-common present using `GOWORK=off go mod vendor`.

Release tags publish `ghcr.io/openpak/np-community`.
