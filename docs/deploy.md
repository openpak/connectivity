# Deploy: connectivity

## What runs

- Internal HTTP API and health on `127.0.0.1:20150` by default.

## Run

```sh
cp config.example.json config.json
chmod 600 config.json
# Edit config.json: replace backend tokens and relay_public_ip before running.
GOWORK=off go run ./cmd/connectivityd -config ./config.json
```

Each token needs at least 32 bytes of independently generated random material;
never reuse a token across backends. Config files must be private (0600) and are
gitignored. Secrets and endpoint values are never written to ordinary logs.

Set both relay ports to `0` to disable the native UDP data plane. TURN allocation
is disabled until resources are configured. Add a resource such as:

```json
{
  "id": "home",
  "country": "IE",
  "stun": "stun.openpak.example:3478",
  "turn": "turn.openpak.example:3478",
  "turn_secret": "REPLACE_WITH_THE_COTURN_NODE_SECRET_AT_LEAST_32_BYTES",
  "healthy": true,
  "home": true
}
```

Only one home is allowed. Resource health is operator-configured at startup;
there is no automatic node discovery or live health probing yet. A country match
wins over home; unhealthy resources are excluded. Credentials expire with the
participant lease, using coturn's REST HMAC-SHA1 format.

`allow_private_endpoints` defaults to false for public relay mappings. Set it
only for a trusted local/test deployment. LAN candidates remain representable
without enabling it; they are never automatically chosen as public relay peers.

## Classic STUN responder (PS3 NAT check)

coturn stays on UDP 3478. RFC 5389 packets carry the magic cookie 0x2112A442 at
payload bytes 4-7, so a host nft rule redirects only cookie-less packets
(`deploy/stun-redirect.nft`, loaded at boot by `deploy/openpak-stun-redirect.service`
from `/etc/openpak/`):

```
udp dport 3478 @th,96,32 != 0x2112a442 redirect to :49161
```

Rollback on the box:

```sh
sudo systemctl disable --now openpak-stun-redirect.service   # ExecStop deletes table ip openpak_stun
sudo rm /etc/systemd/system/openpak-stun-redirect.service /etc/openpak/stun-redirect.nft; sudo systemctl daemon-reload
cp ~/openpak/deploy/connectivity/releases/20261021-stun/openpak-connectivity.container.before \
   ~/.config/containers/systemd/openpak-connectivity.container
systemctl --user daemon-reload && systemctl --user restart openpak-connectivity   # back to 20261013-npln
```

Step 1 alone already restores the coturn-only behaviour on 3478.

## Deploy and validate

`Dockerfile` builds amd64/arm64; `deploy/compose.yml` is a host-network deployment
template. Provision the private config so the container's UID 65532 can read it
(ownership mapping must match the container runtime). The API should remain on
loopback/private authenticated infrastructure; use a TLS reverse proxy for calls
across hosts. Never expose the bearer-token API to consoles or the public internet.
Publish only the configured UDP relay pool, with host/provider firewall rules.
The example uses `22900–22979/udp`; public IP and NAT forwarding must match.

```sh
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go build ./cmd/connectivityd
podman build -t localhost/openpak-connectivity:local .
python3 tools/smoke_container.py
```
